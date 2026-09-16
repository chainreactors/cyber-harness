// Package hooks provides typed execution extension points with explicit result
// semantics and error policies.
//
// A hook point is a package-level Point[E, R] descriptor carrying both type
// parameters, so callers write ToolCallHook.Emit(ctx, reg, ev) without spelling
// out E and R. The Registry stores handlers type-erased and is copy-on-write:
// registration takes a mutex, dispatch is a single atomic load. Tool calls run
// from N goroutines concurrently, so Emit must never block on registration.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

type Kind string

// identity is the in-process identity of a Point. Kind remains a diagnostic
// name only, so two packages cannot accidentally share handlers by reusing a
// string.
type identity struct{ value byte }

type ErrorPolicy uint8

const (
	ContinueOnError ErrorPolicy = iota // collect, report, keep dispatching
	FailClosed                         // first error aborts dispatch; caller must deny
)

// HandlerError attributes a failure to the handler that produced it. Handlers
// are registered with a mandatory source so a hook failure never has to be
// traced back by hand.
type HandlerError struct {
	Source string
	Kind   Kind
	Err    error
	Panic  any
	Stack  []byte
}

func (e *HandlerError) Error() string {
	return fmt.Sprintf("hook %s/%s: %v", e.Kind, e.Source, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

// Reducer folds one handler result into the accumulated result. ev is a pointer
// so fold-style points can let the next handler observe the previous handler's
// change. Returning true short-circuits the remaining handlers.
type Reducer[E any, R any] func(acc *R, ev *E, out R) (stop bool)

type Point[E any, R any] struct {
	id      *identity
	kind    Kind
	reduce  Reducer[E, R] // nil => pure observation
	onError ErrorPolicy
}

// NewPoint creates one typed hook boundary. The returned value may be copied;
// copies retain the same private identity.
func NewPoint[E any, R any](kind Kind) Point[E, R] {
	if kind == "" {
		panic("hooks: point name is required")
	}
	return Point[E, R]{id: &identity{}, kind: kind}
}

func (p Point[E, R]) WithReducer(reducer Reducer[E, R]) Point[E, R] {
	p.reduce = reducer
	return p
}

func (p Point[E, R]) WithErrorPolicy(policy ErrorPolicy) Point[E, R] {
	p.onError = policy
	return p
}

func (p Point[E, R]) Name() Kind { return p.kind }

func (p Point[E, R]) Has(r *Registry) bool { return p.Len(r) > 0 }

func (p Point[E, R]) Len(r *Registry) int {
	if r == nil || p.id == nil {
		return 0
	}
	t := r.handlers.Load()
	if t == nil {
		return 0
	}
	return len(t.byPoint[p.id])
}

type entry struct {
	source string
	gate   *Subscription
	fn     any // func(context.Context, E) (R, error), asserted back in dispatch
}

// table is replaced wholesale on every registration change; readers only ever
// see a consistent immutable snapshot.
type table struct {
	byPoint map[*identity][]entry
}

type Registry struct {
	mu sync.Mutex

	handlers atomic.Pointer[table]
}

func New() *Registry {
	r := &Registry{}
	r.handlers.Store(&table{byPoint: map[*identity][]entry{}})
	return r
}

// Clear drops every handler. Modules own their unsubscribe handles and other
// resources; the registry does not collect unrelated cleanup callbacks.
func (r *Registry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	old := r.handlers.Load()
	r.handlers.Store(&table{byPoint: map[*identity][]entry{}})
	r.mu.Unlock()
	if old != nil {
		for _, entries := range old.byPoint {
			for _, e := range entries {
				e.gate.Cancel()
			}
		}
	}
}

func (r *Registry) add(point *identity, source string, fn any) *Subscription {
	r.mu.Lock()
	sub := &Subscription{done: make(chan struct{})}
	sub.remove = func() { r.remove(point, sub) }
	next := r.cloneLocked()
	prev := next.byPoint[point]
	list := make([]entry, len(prev), len(prev)+1)
	copy(list, prev)
	next.byPoint[point] = append(list, entry{source: source, fn: fn, gate: sub})
	r.handlers.Store(next)
	r.mu.Unlock()

	return sub
}

func (r *Registry) remove(point *identity, subscription *Subscription) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.handlers.Load()
	if old == nil {
		return
	}
	prev := old.byPoint[point]
	idx := -1
	for i := range prev {
		if prev[i].gate == subscription {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	next := r.cloneLocked()
	if len(prev) == 1 {
		delete(next.byPoint, point)
	} else {
		list := make([]entry, 0, len(prev)-1)
		list = append(list, prev[:idx]...)
		list = append(list, prev[idx+1:]...)
		next.byPoint[point] = list
	}
	r.handlers.Store(next)
}

// cloneLocked copies the kind map; the per-kind slices stay shared because an
// in-flight dispatch may still be iterating them.
func (r *Registry) cloneLocked() *table {
	old := r.handlers.Load()
	if old == nil {
		return &table{byPoint: make(map[*identity][]entry, 1)}
	}
	next := &table{byPoint: make(map[*identity][]entry, len(old.byPoint)+1)}
	for k, v := range old.byPoint {
		next.byPoint[k] = v
	}
	return next
}

// On registers a handler owned by its returned subscription. Cancel revokes
// admission, including in old dispatch snapshots; Close also drains callbacks.
// Registration requires a registry, source and function. Emit on a nil registry
// remains the optional, allocation-free execution fast path.
func (p Point[E, R]) On(r *Registry, source string, fn func(context.Context, E) (R, error)) *Subscription {
	if p.id == nil {
		panic("hooks: Point must be created with NewPoint")
	}
	if source == "" {
		panic("hooks: On requires a non-empty source for " + string(p.kind))
	}
	if fn == nil {
		panic("hooks: On requires a non-nil handler for " + string(p.kind))
	}
	if r == nil {
		panic("hooks: On requires a registry")
	}
	return r.add(p.id, source, fn)
}

// Emit runs the point's handlers sequentially in registration order and folds
// their results through Reduce. With no handlers it returns the zero result
// without allocating.
func (p Point[E, R]) Emit(ctx context.Context, r *Registry, ev E) (R, error) {
	var zero R
	if r == nil {
		return zero, nil
	}
	t := r.handlers.Load()
	if t == nil {
		return zero, nil
	}
	entries := t.byPoint[p.id]
	if len(entries) == 0 {
		return zero, nil
	}
	return p.dispatch(ctx, r, entries, ev)
}

// dispatch is kept out of Emit so that taking &ev here does not force Emit's
// argument onto the heap on the zero-handler path.
//
//go:noinline
func (p Point[E, R]) dispatch(ctx context.Context, r *Registry, entries []entry, ev E) (R, error) {
	var acc R
	var errs []error

	// Snapshots preserve registration order, but admission is checked per handler.
	for _, e := range entries {
		if !e.gate.begin() {
			continue
		}
		// A private Point identity can only be registered through this exact
		// E/R instantiation, so this assertion is guaranteed by construction.
		fn := e.fn.(func(context.Context, E) (R, error))
		out, panicValue, stack, err := invokeHandler(ctx, fn, ev, e.gate)
		if err != nil {
			he := &HandlerError{Source: e.source, Kind: p.kind, Err: err, Panic: panicValue, Stack: stack}
			errs = append(errs, he)
			if p.onError == FailClosed {
				return acc, errors.Join(errs...)
			}
			continue
		}
		if p.reduce == nil {
			continue
		}
		if p.reduce(&acc, &ev, out) {
			break
		}
	}
	return acc, errors.Join(errs...)
}

func invokeHandler[E any, R any](ctx context.Context, fn func(context.Context, E) (R, error), ev E, sub *Subscription) (out R, panicValue any, stack []byte, err error) {
	defer sub.end()
	defer func() {
		if recovered := recover(); recovered != nil {
			var zero R
			out = zero
			err = errors.New("handler panicked")
			panicValue = recovered
			stack = debug.Stack()
		}
	}()
	out, err = fn(ctx, ev)
	return out, nil, nil, err
}
