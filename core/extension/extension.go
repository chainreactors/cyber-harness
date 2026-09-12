package extension

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrCloseIncomplete marks cleanup that must be retried before dependencies
// can be released. A Close error without this marker means cleanup completed.
var ErrCloseIncomplete = errors.New("extension cleanup incomplete")

// Extension owns a resource or a contribution with an independent lifetime.
// Constructors must be inert. Load and Close must not call the owning Set.
type Extension interface {
	Load(*Context) error
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
	ctx         *Context
	cleanupDone bool
}

// Set serializes the lifecycle of a fixed dependency graph. A failed Load seals
// the Set; incomplete cleanup can be retried with a fresh Close context.
type Set struct {
	gate    chan struct{}
	items   map[string]*item
	order   []string
	closing bool
}

// New validates and orders entries without calling extensions. Entries must not
// contain typed nils or reuse an instance across entries or Sets.
func New(entries ...Entry) (*Set, error) {
	s := &Set{gate: make(chan struct{}, 1), items: make(map[string]*item, len(entries))}
	for _, e := range entries {
		if e.ID == "" || e.Extension == nil {
			return nil, fmt.Errorf("extension entry requires id and extension")
		}
		if _, ok := s.items[e.ID]; ok {
			return nil, fmt.Errorf("duplicate extension: %s", e.ID)
		}
		s.items[e.ID] = &item{extension: e.Extension, deps: slices.Clone(e.DependsOn)}
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
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	if s.closing {
		return fmt.Errorf("extension set is closing or closed")
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
			return errors.Join(err, s.closeReverse(ctx, started))
		}
		it.state = loadingState
		started = append(started, id)
		if it.ctx == nil {
			it.ctx = newContext(ctx, id)
		}
		if err := it.extension.Load(it.ctx); err != nil {
			s.closing = true
			return errors.Join(fmt.Errorf("load extension %s: %w", id, err), s.closeReverse(ctx, started))
		}
		if err := ctx.Err(); err != nil {
			s.closing = true
			return errors.Join(err, s.closeReverse(ctx, started))
		}
		it.state = activeState
	}
	return nil
}
func (s *Set) Close(ctx context.Context) error {
	if err := s.lock(ctx); err != nil {
		return errors.Join(ErrCloseIncomplete, err)
	}
	defer func() { <-s.gate }()
	s.closing = true
	return s.closeReverse(ctx, s.order)
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
		if it.ctx != nil {
			stopErr = it.ctx.stop()
			if stopErr != nil {
				errs = append(errs, fmt.Errorf("stop extension %s: %w", id, errors.Join(ErrCloseIncomplete, stopErr)))
			}
		}
		if !it.cleanupDone {
			if err := it.extension.Close(ctx); err != nil {
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
