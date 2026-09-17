package extension

import (
	"context"
	"errors"
	"sync"

	"github.com/chainreactors/cyber/core/resource"
)

// Scope owns typed registration handles created by one Extension.
//
// Outgoing registrations and incoming borrows unwind at different moments, so
// they are kept apart. What this Extension published (Define, Add, Provide)
// closes before its own Close; what it borrowed (Use) releases after, because
// Close is exactly where an Extension drains the work still calling a borrowed
// value.
type Scope struct {
	init      context.Context
	lifetime  context.Context
	cancel    context.CancelFunc
	resources *resource.Registry
	mu        sync.Mutex
	handles   []resource.Handle
	borrows   []resource.Handle
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

// Provide offers the single value of type T to later Extensions. Always write
// the type argument explicitly: inference registers the concrete type, which
// is rarely the key consumers name.
func Provide[T any](s *Scope, value T) error {
	if s == nil {
		return resource.ErrInvalid
	}
	return s.register(func() (resource.Handle, error) {
		return resource.Provide[T](s.resources, value)
	})
}

// Use borrows the value of type T published by an earlier Extension. It fails
// when nothing provides T, so a wiring mistake is a load error naming the type
// rather than a nil value discovered later.
func Use[T any](s *Scope) (T, error) {
	var zero T
	if s == nil {
		return zero, resource.ErrInvalid
	}
	value, handle, err := resource.Use[T](s.resources)
	if err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return zero, errors.Join(context.Canceled, handle.Close(context.Background()))
	}
	s.borrows = append(s.borrows, handle)
	return value, nil
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

// release unwinds this Extension's borrows, after its own Close has returned.
func (s *Scope) release(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for len(s.borrows) > 0 {
		index := len(s.borrows) - 1
		err := s.borrows[index].Close(ctx)
		if err != nil {
			errs = append(errs, err)
			if errors.Is(err, resource.ErrCloseIncomplete) {
				return errors.Join(errs...)
			}
		}
		s.borrows = s.borrows[:index]
	}
	return errors.Join(errs...)
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
