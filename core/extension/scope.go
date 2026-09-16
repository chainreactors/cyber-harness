package extension

import (
	"context"
	"errors"
	"sync"

	"github.com/chainreactors/cyber/core/resource"
)

// Scope owns typed registration handles created by one Extension.
type Scope struct {
	init      context.Context
	lifetime  context.Context
	cancel    context.CancelFunc
	resources *resource.Registry
	mu        sync.Mutex
	handles   []resource.Handle
	stopped   bool
}

func newScope(init context.Context, resources *resource.Registry) *Scope {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Scope{init: init, lifetime: lifetime, cancel: cancel, resources: resources}
}

func (s *Scope) Init() context.Context     { return s.init }
func (s *Scope) Lifetime() context.Context { return s.lifetime }

func Define[T any](s *Scope, point resource.Point[T]) error {
	if s == nil {
		return resource.ErrInvalid
	}
	return s.register(func() (resource.Handle, error) {
		return resource.Define(s.resources, point)
	})
}

func Add[T any](s *Scope, values ...T) error {
	if s == nil {
		return resource.ErrInvalid
	}
	return s.register(func() (resource.Handle, error) {
		return resource.Add(s.resources, values...)
	})
}

func (s *Scope) register(register func() (resource.Handle, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return context.Canceled
	}
	handle, err := register()
	if err != nil {
		return err
	}
	s.handles = append(s.handles, handle)
	return nil
}

func (s *Scope) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		s.cancel()
	}
	s.mu.Unlock()
}

func (s *Scope) close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for len(s.handles) > 0 {
		index := len(s.handles) - 1
		err := s.handles[index].Close(ctx)
		if err != nil {
			errs = append(errs, err)
			if errors.Is(err, resource.ErrCloseIncomplete) {
				return errors.Join(errs...)
			}
		}
		s.handles = s.handles[:index]
	}
	return errors.Join(errs...)
}
