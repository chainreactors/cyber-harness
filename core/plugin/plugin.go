// Package plugin coordinates explicitly constructed modules. It has no service
// lookup or execution API; consumers receive dependencies from constructors.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/chainreactors/aiscan/core/capability"
)

// Module owns its resources. Construction must have no side effects. Close
// must handle partial Load, stop admission, cancel and wait for work, and release
// resources. On error it must permit a subsequent Close to finish cleanup.
// Methods must cooperate with ctx and must not reenter their owning Set.
// Load's context bounds initialization, not the module's lifetime. A module
// owns the cancellation of its ongoing work. Close's context bounds this
// cleanup attempt; timeout does not transfer ownership to the coordinator.
type Module interface {
	Load(context.Context) error
	Close(context.Context) error
}

// Entry binds a descriptor to an already constructed module. Each instance must
// belong to exactly one Entry and Set; ownership is the caller's responsibility.
type Entry struct {
	Descriptor capability.Descriptor
	Module     Module
}

type state uint8

const (
	newState state = iota
	loadingState
	activeState
	stoppingState
	closedState
)

// item is the actual owned instance and its lifecycle state, not a result mirror.
type item struct {
	module Module
	deps   []capability.ID
	state  state
}

// Set serializes lifecycle transactions, never business execution. Construct
// with New. Entries are fixed; closed instances cannot be loaded again.
type Set struct {
	gate    chan struct{}
	items   map[capability.ID]*item
	order   []capability.ID
	closing bool
}

// New validates the entire graph without calling modules. Declaration order
// breaks ties between independent modules. Dependency slices are copied.
func New(entries ...Entry) (*Set, error) {
	s := &Set{gate: make(chan struct{}, 1), items: make(map[capability.ID]*item, len(entries))}
	for _, entry := range entries {
		id := entry.Descriptor.ID
		if id == "" || entry.Module == nil {
			return nil, fmt.Errorf("plugin entry requires id and module")
		}
		if _, exists := s.items[id]; exists {
			return nil, fmt.Errorf("duplicate plugin: %s", id)
		}
		s.items[id] = &item{module: entry.Module, deps: slices.Clone(entry.Descriptor.DependsOn)}
	}
	visiting, visited := map[capability.ID]bool{}, map[capability.ID]bool{}
	var visit func(capability.ID) error
	visit = func(id capability.ID) error {
		it, ok := s.items[id]
		if !ok {
			return fmt.Errorf("missing plugin: %s", id)
		}
		if visiting[id] {
			return fmt.Errorf("plugin dependency cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range it.deps {
			if err := visit(dep); err != nil {
				return fmt.Errorf("plugin %s: %w", id, err)
			}
		}
		delete(visiting, id)
		visited[id] = true
		s.order = append(s.order, id)
		return nil
	}
	for _, entry := range entries {
		if err := visit(entry.Descriptor.ID); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// lock allows a caller to cancel while another lifecycle transaction is running.
func (s *Set) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		// The context may have been canceled while waiting for the gate. Do
		// not start or mutate a lifecycle transaction in that case.
		if err := ctx.Err(); err != nil {
			<-s.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Load starts the named modules and their dependencies. No IDs means no work.
// Already active modules are reused. Rollback uses the caller's context; if
// cleanup cannot finish, later Unload or Close must retry with a fresh context.
func (s *Set) Load(ctx context.Context, ids ...capability.ID) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	if s.closing {
		return fmt.Errorf("plugin set is closing or closed")
	}
	wanted := make(map[capability.ID]bool)
	var selectModule func(capability.ID) error
	selectModule = func(id capability.ID) error {
		it, ok := s.items[id]
		if !ok {
			return fmt.Errorf("unknown plugin: %s", id)
		}
		if wanted[id] {
			return nil
		}
		if it.state != newState && it.state != activeState {
			return fmt.Errorf("plugin %s is stopping or closed", id)
		}
		wanted[id] = true
		for _, dep := range it.deps {
			if err := selectModule(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range ids {
		if err := selectModule(id); err != nil {
			return err
		}
	}
	var started []capability.ID
	for _, id := range s.order {
		it := s.items[id]
		if !wanted[id] || it.state == activeState {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, s.closeReverse(ctx, started))
		}
		it.state = loadingState
		started = append(started, id)
		err := it.module.Load(ctx)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return errors.Join(fmt.Errorf("load plugin %s: %w", id, err), s.closeReverse(ctx, started))
		}
		it.state = activeState
	}
	return nil
}

// Unload rejects live dependents before changing state. A failed Close retains
// the instance and its dependency claims; subsequent calls retry cleanup.
func (s *Set) Unload(ctx context.Context, id capability.ID) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	return s.unload(ctx, id)
}

func (s *Set) unload(ctx context.Context, id capability.ID) error {
	it, ok := s.items[id]
	if !ok {
		return fmt.Errorf("unknown plugin: %s", id)
	}
	if it.state == closedState {
		return nil
	}
	for _, otherID := range s.order {
		other := s.items[otherID]
		if other.state != newState && other.state != closedState && slices.Contains(other.deps, id) {
			return fmt.Errorf("plugin %s is required by live plugin %s", id, otherID)
		}
	}
	if it.state == newState {
		it.state = closedState
		return nil
	}
	it.state = stoppingState
	if err := it.module.Close(ctx); err != nil {
		return fmt.Errorf("close plugin %s: %w", id, err)
	}
	it.state = closedState
	return nil
}

// Close permanently stops new loads and closes in reverse dependency order.
// Failed modules retain their dependencies; unrelated modules can still close.
// Successful closures are not repeated. A subsequent Close retries failures.
func (s *Set) Close(ctx context.Context) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	s.closing = true
	return s.closeReverse(ctx, s.order)
}

func (s *Set) closeReverse(ctx context.Context, ids []capability.ID) error {
	var errs []error
	for i := len(ids) - 1; i >= 0; i-- {
		if err := s.unload(ctx, ids[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
