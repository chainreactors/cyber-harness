package extension

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
)

// ErrCloseIncomplete marks cleanup that must be retried before dependencies
// can be released. A Close error without this marker means cleanup completed.
var ErrCloseIncomplete = errors.New("extension cleanup incomplete")

// Extension owns a resource or a contribution with an independent lifetime.
// Constructors must be inert. Load and Close must not call the owning Set.
type Extension interface {
	Load(*Scope) error
	Close(context.Context) error
}

// Entry declares lifecycle ordering. Dependencies are injected by constructors,
// never looked up through DependsOn or Context. Each instance has one owner.
type Entry struct {
	ID        string
	DependsOn []string
	Extension Extension
}
type state uint8

const (
	newState state = iota
	loadingState
	activeState
	stoppingState
	closedState
)

type item struct {
	extension   Extension
	deps        []string
	state       state
	scope       *Scope
	cleanupDone bool
}

// Set serializes the lifecycle of a fixed dependency graph. A failed Load seals
// the Set; incomplete cleanup can be retried with a fresh Close context.
type Set struct {
	gate    chan struct{}
	items   map[string]*item
	order   []string
	closing bool
	claims  []instanceClaim
	claimed bool
}

type instanceID struct {
	typeName string
	pointer  uintptr
}

type instanceClaim struct {
	identity instanceID
	entry    string
}

var instanceClaims = struct {
	sync.Mutex
	owners map[instanceID]*Set
}{owners: make(map[instanceID]*Set)}

// New validates and orders entries without calling extensions. Entries must not
// contain typed nils or reuse an instance within the graph. Load atomically
// rejects reuse by another live Set.
func New(entries ...Entry) (*Set, error) {
	s := &Set{gate: make(chan struct{}, 1), items: make(map[string]*item, len(entries))}
	identities := make(map[instanceID]string)
	for _, e := range entries {
		if e.ID == "" || isNilExtension(e.Extension) {
			return nil, fmt.Errorf("extension entry requires id and extension")
		}
		if _, ok := s.items[e.ID]; ok {
			return nil, fmt.Errorf("duplicate extension: %s", e.ID)
		}
		s.items[e.ID] = &item{extension: e.Extension, deps: slices.Clone(e.DependsOn)}
		if identity, ok := extensionInstanceID(e.Extension); ok {
			if previous, exists := identities[identity]; exists {
				return nil, fmt.Errorf("extension instance is reused by %s and %s", previous, e.ID)
			}
			identities[identity] = e.ID
			s.claims = append(s.claims, instanceClaim{identity: identity, entry: e.ID})
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		it, ok := s.items[id]
		if !ok {
			return fmt.Errorf("missing extension: %s", id)
		}
		if visiting[id] {
			return fmt.Errorf("extension dependency cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range it.deps {
			if err := visit(dep); err != nil {
				return fmt.Errorf("extension %s: %w", id, err)
			}
		}
		delete(visiting, id)
		visited[id] = true
		s.order = append(s.order, id)
		return nil
	}
	for _, e := range entries {
		if err := visit(e.ID); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Set) lock(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Set) Load(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	if s.closing {
		return fmt.Errorf("extension set is closing or closed")
	}
	if err := s.claimInstances(); err != nil {
		return err
	}
	var started []string
	for _, id := range s.order {
		it := s.items[id]
		if it.state == activeState {
			continue
		}
		if it.state != newState {
			return fmt.Errorf("extension %s is stopping or closed", id)
		}
		if err := ctx.Err(); err != nil {
			s.closing = true
			closeErr := s.closeReverse(ctx, started)
			s.releaseClaimsIfClosed()
			return errors.Join(err, closeErr)
		}
		it.state = loadingState
		started = append(started, id)
		if it.scope == nil {
			it.scope = newScope(ctx)
		}
		loadErr := invokeLoad(it.extension, it.scope)
		if err := loadErr; err != nil {
			s.closing = true
			closeErr := s.closeReverse(ctx, started)
			s.releaseClaimsIfClosed()
			return errors.Join(fmt.Errorf("load extension %s: %w", id, err), closeErr)
		}
		if err := ctx.Err(); err != nil {
			s.closing = true
			closeErr := s.closeReverse(ctx, started)
			s.releaseClaimsIfClosed()
			return errors.Join(err, closeErr)
		}
		it.state = activeState
	}
	return nil
}
func (s *Set) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.lock(ctx); err != nil {
		return errors.Join(ErrCloseIncomplete, err)
	}
	defer func() { <-s.gate }()
	s.closing = true
	err := s.closeReverse(ctx, s.order)
	s.releaseClaimsIfClosed()
	return err
}
func (s *Set) closeReverse(ctx context.Context, ids []string) error {
	var errs []error
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		it := s.items[id]
		if it.state == closedState {
			continue
		}
		blocked := false
		for _, o := range s.items {
			if o.state != newState && o.state != closedState && slices.Contains(o.deps, id) {
				blocked = true
				break
			}
		}
		if blocked {
			errs = append(errs, fmt.Errorf("close extension %s: live dependent did not close: %w", id, ErrCloseIncomplete))
			continue
		}
		if it.state == newState {
			it.state = closedState
			continue
		}
		it.state = stoppingState
		var stopErr error
		if it.scope != nil {
			stopErr = it.scope.stop()
			if stopErr != nil {
				errs = append(errs, fmt.Errorf("stop extension %s: %w", id, errors.Join(ErrCloseIncomplete, stopErr)))
			}
		}
		if !it.cleanupDone {
			if err := incompleteOnCancellation(invokeClose(it.extension, ctx)); err != nil {
				errs = append(errs, fmt.Errorf("close extension %s: %w", id, err))
				if errors.Is(err, ErrCloseIncomplete) {
					continue
				}
			}
			it.cleanupDone = true
		}
		if stopErr != nil {
			// A panicking revocation leaves registration state uncertain. Close
			// still gets a chance to drain, but dependencies stay protected.
			continue
		}
		it.state = closedState
	}
	return errors.Join(errs...)
}

// incompleteOnCancellation keeps the retry contract in the lifecycle owner.
// An extension that returns its Close context error necessarily did not finish
// within that cleanup attempt; adapters must not all repeat this conversion.
func incompleteOnCancellation(err error) error {
	if err == nil || errors.Is(err, ErrCloseIncomplete) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrCloseIncomplete, err)
	}
	return err
}

func invokeLoad(value Extension, scope *Scope) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("extension Load panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return value.Load(scope)
}

func invokeClose(value Extension, ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.Join(ErrCloseIncomplete, fmt.Errorf("extension Close panicked: %v\n%s", recovered, debug.Stack()))
		}
	}()
	return value.Close(ctx)
}

func isNilExtension(value Extension) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func extensionInstanceID(value Extension) (instanceID, bool) {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer {
		return instanceID{}, false
	}
	return instanceID{typeName: v.Type().String(), pointer: v.Pointer()}, true
}

func (s *Set) claimInstances() error {
	if s.claimed {
		return nil
	}
	instanceClaims.Lock()
	defer instanceClaims.Unlock()
	for _, claim := range s.claims {
		if owner := instanceClaims.owners[claim.identity]; owner != nil && owner != s {
			return fmt.Errorf("extension %s instance already belongs to another set", claim.entry)
		}
	}
	for _, claim := range s.claims {
		instanceClaims.owners[claim.identity] = s
	}
	s.claimed = true
	return nil
}

func (s *Set) releaseClaimsIfClosed() {
	if !s.claimed {
		return
	}
	for _, it := range s.items {
		if it.state != closedState {
			return
		}
	}
	instanceClaims.Lock()
	defer instanceClaims.Unlock()
	if !s.claimed {
		return
	}
	for _, claim := range s.claims {
		if instanceClaims.owners[claim.identity] == s {
			delete(instanceClaims.owners, claim.identity)
		}
	}
	s.claimed = false
}
