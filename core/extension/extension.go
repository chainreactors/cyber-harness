package extension

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync/atomic"

	"github.com/chainreactors/cyber/core/resource"
)

var ErrCloseIncomplete = resource.ErrCloseIncomplete

// Extension is one fixed-lifetime part of a Profile. Constructor injection
// expresses dependencies; declaration order expresses lifecycle order. An
// Extension that owns work or open resources may additionally implement
// Close(context.Context) error.
type Extension interface {
	Load(*Scope) error
}

type closer interface {
	Close(context.Context) error
}

type item struct {
	extension Extension
	scope     *Scope
}

// Set loads a fixed list in declaration order and closes it in reverse order.
// It deliberately has no IDs, graph, service table, or instance ownership.
type Set struct {
	gate      chan struct{}
	items     []item
	resources *resource.Registry
	closing   atomic.Bool
	active    atomic.Bool
	sealed    bool
	nextClose int
}

func New(extensions ...Extension) (*Set, error) {
	items := make([]item, len(extensions))
	for i, value := range extensions {
		if isNilExtension(value) {
			return nil, fmt.Errorf("extension %d is nil", i)
		}
		items[i].extension = value
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Set{items: items, resources: resource.New(), gate: gate, nextClose: -1}, nil
}

func (s *Set) Load(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("extension set is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	if s.closing.Load() || s.sealed {
		return fmt.Errorf("extension set is closing, closed, or already loaded")
	}
	s.sealed = true
	for i := range s.items {
		it := &s.items[i]
		if err := ctx.Err(); err != nil {
			s.closing.Store(true)
			return errors.Join(err, s.closeFrom(context.WithoutCancel(ctx), i-1))
		}
		s.nextClose = i
		it.scope = newScope(ctx, s.resources.Scope(i+1))
		if err := invokeLoad(it.extension, it.scope); err != nil {
			s.closing.Store(true)
			return errors.Join(fmt.Errorf("load extension %d (%T): %w", i, it.extension, err), s.closeFrom(context.WithoutCancel(ctx), i))
		}
		if s.closing.Load() {
			return errors.Join(fmt.Errorf("extension set closed during load"), s.closeFrom(context.WithoutCancel(ctx), i))
		}
	}
	s.resources.Freeze()
	s.active.Store(true)
	return nil
}

func (s *Set) Active() bool {
	return s != nil && s.active.Load() && !s.closing.Load()
}

func (s *Set) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.closing.Store(true)
	s.active.Store(false)
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrCloseIncomplete, err)
	}
	if err := s.enter(ctx); err != nil {
		return errors.Join(ErrCloseIncomplete, err)
	}
	defer s.leave()
	return s.closeFrom(ctx, s.nextClose)
}

func (s *Set) enter(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
		return nil
	}
}

func (s *Set) leave() { s.gate <- struct{}{} }

func (s *Set) closeFrom(ctx context.Context, start int) error {
	var errs []error
	for i := start; i >= 0; i-- {
		it := &s.items[i]
		if it.scope != nil {
			it.scope.stop()
			if err := it.scope.close(ctx); err != nil {
				err = incompleteOnCancellation(err)
				errs = append(errs, fmt.Errorf("close resources for extension %d (%T): %w", i, it.extension, err))
				if errors.Is(err, ErrCloseIncomplete) {
					s.nextClose = i
					return errors.Join(errs...)
				}
			}
		}
		if err := incompleteOnCancellation(invokeClose(it.extension, ctx)); err != nil {
			errs = append(errs, fmt.Errorf("close extension %d (%T): %w", i, it.extension, err))
			if errors.Is(err, ErrCloseIncomplete) {
				s.nextClose = i
				return errors.Join(errs...)
			}
		}
		// Borrows release last. An Extension drains the work still calling a
		// borrowed value inside its own Close, so releasing earlier would let
		// the provider shut down underneath it.
		if it.scope != nil {
			if err := incompleteOnCancellation(it.scope.release(ctx)); err != nil {
				errs = append(errs, fmt.Errorf("release borrows for extension %d (%T): %w", i, it.extension, err))
				if errors.Is(err, ErrCloseIncomplete) {
					s.nextClose = i
					return errors.Join(errs...)
				}
			}
		}
		s.nextClose = i - 1
	}
	return errors.Join(errs...)
}

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
	owner, ok := value.(closer)
	if !ok {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.Join(ErrCloseIncomplete, fmt.Errorf("extension Close panicked: %v\n%s", recovered, debug.Stack()))
		}
	}()
	return owner.Close(ctx)
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
