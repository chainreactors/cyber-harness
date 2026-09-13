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

// ErrTypeMismatch means two Points share a Kind with different E/R. Reporting it
// as a handler failure keeps the handler from silently vanishing.
var ErrTypeMismatch = errors.New("handler signature does not match hook point")

// Reducer folds one handler result into the accumulated result. ev is a pointer
// so fold-style points can let the next handler observe the previous handler's
// change. Returning true short-circuits the remaining handlers.
type Reducer[E any, R any] func(acc *R, ev *E, out R) (stop bool)

type Point[E any, R any] struct {
	Kind    Kind
	Reduce  Reducer[E, R] // nil => pure observation
	OnError ErrorPolicy
}

type entry struct {
	id     uint64
	source string
	gate   *Subscription
	fn     any // func(context.Context, E) (R, error), asserted back in dispatch
}

// table is replaced wholesale on every registration change; readers only ever
// see a consistent immutable snapshot.
type table struct {
	byKind map[Kind][]entry
}

type Registry struct {
	mu     sync.Mutex
	nextID uint64

	handlers atomic.Pointer[table]
}

func New() *Registry {
	r := &Registry{}
	r.handlers.Store(&table{byKind: map[Kind][]entry{}})
	return r
}

// Has is the zero-handler fast path: one atomic load plus a map lookup, no locks
// and no allocations.
func (r *Registry) Has(kind Kind) bool {
	return r.Len(kind) > 0
}

func (r *Registry) Len(kind Kind) int {
	if r == nil {
		return 0
	}
	t := r.handlers.Load()
	if t == nil {
		return 0
	}
	return len(t.byKind[kind])
}

// Clear drops every handler. Modules own their unsubscribe handles and other
// resources; the registry does not collect unrelated cleanup callbacks.
func (r *Registry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	old := r.handlers.Load()
	r.handlers.Store(&table{byKind: map[Kind][]entry{}})
	r.mu.Unlock()
	if old != nil {
		for _, entries := range old.byKind {
			for _, e := range entries {
				e.gate.Cancel()
			}
		}
	}
}

func (r *Registry) add(kind Kind, source string, fn any) *Subscription {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	sub := &Subscription{done: make(chan struct{}), remove: func() { r.remove(kind, id) }}
	next := r.cloneLocked()
	prev := next.byKind[kind]
	list := make([]entry, len(prev), len(prev)+1)
	copy(list, prev)
	next.byKind[kind] = append(list, entry{id: id, source: source, fn: fn, gate: sub})
	r.handlers.Store(next)
	r.mu.Unlock()

	return sub
}

func (r *Registry) remove(kind Kind, id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.handlers.Load()
	if old == nil {
		return
	}
	prev := old.byKind[kind]
	idx := -1
	for i := range prev {
		if prev[i].id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	next := r.cloneLocked()
	if len(prev) == 1 {
		delete(next.byKind, kind)
	} else {
		list := make([]entry, 0, len(prev)-1)
		list = append(list, prev[:idx]...)
		list = append(list, prev[idx+1:]...)
		next.byKind[kind] = list
	}
	r.handlers.Store(next)
}

// cloneLocked copies the kind map; the per-kind slices stay shared because an
// in-flight dispatch may still be iterating them.
func (r *Registry) cloneLocked() *table {
	old := r.handlers.Load()
	if old == nil {
		return &table{byKind: make(map[Kind][]entry, 1)}
	}
	next := &table{byKind: make(map[Kind][]entry, len(old.byKind)+1)}
	for k, v := range old.byKind {
		next.byKind[k] = v
	}
	return next
}

// On registers a handler owned by its returned subscription. Cancel revokes
// admission, including in old dispatch snapshots; Close also drains callbacks.
// Registration requires a registry, source and function. Emit on a nil registry
// remains the optional, allocation-free execution fast path.
func (p Point[E, R]) On(r *Registry, source string, fn func(context.Context, E) (R, error)) *Subscription {
	if source == "" {
		panic("hooks: On requires a non-empty source for " + string(p.Kind))
	}
	if fn == nil {
		panic("hooks: On requires a non-nil handler for " + string(p.Kind))
	}
	if r == nil {
		panic("hooks: On requires a registry")
	}
	return r.add(p.Kind, source, fn)
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
	entries := t.byKind[p.Kind]
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
		fn, ok := e.fn.(func(context.Context, E) (R, error))
		if !ok {
			e.gate.end()
			he := &HandlerError{Source: e.source, Kind: p.Kind, Err: ErrTypeMismatch}
			errs = append(errs, he)
			if p.OnError == FailClosed {
				return acc, errors.Join(errs...)
			}
			continue
		}
		out, panicValue, stack, err := invokeHandler(ctx, fn, ev, e.gate)
		if err != nil {
			he := &HandlerError{Source: e.source, Kind: p.Kind, Err: err, Panic: panicValue, Stack: stack}
			errs = append(errs, he)
			if p.OnError == FailClosed {
				return acc, errors.Join(errs...)
			}
			continue
		}
		if p.Reduce == nil {
			continue
		}
		if p.Reduce(&acc, &ev, out) {
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
