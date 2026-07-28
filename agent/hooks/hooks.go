// Package hooks is the agent kernel's single extension mechanism: typed hook
// points with explicit result semantics and error policies.
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
}

func (e *HandlerError) Error() string {
	return fmt.Sprintf("hook %s/%s: %v", e.Kind, e.Source, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

// errTypeMismatch means two Points share a Kind with different E/R. Reporting it
// as a handler failure keeps the handler from silently vanishing.
var errTypeMismatch = errors.New("handler signature does not match hook point")

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
	fn     any // func(context.Context, E) (R, error), asserted back in dispatch
}

// table is replaced wholesale on every registration change; readers only ever
// see a consistent immutable snapshot.
type table struct {
	byKind map[Kind][]entry
}

type errorSink struct {
	fn func(*HandlerError)
}

type cleanup struct {
	id uint64
	fn func()
}

type Registry struct {
	mu       sync.Mutex
	nextID   uint64
	cleanups []cleanup

	handlers atomic.Pointer[table]
	sink     atomic.Pointer[errorSink]
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

// SetErrorSink installs the reporter for handler failures. Passing nil disables
// reporting; errors are still collected and returned by Emit.
func (r *Registry) SetErrorSink(fn func(*HandlerError)) {
	if r == nil {
		return
	}
	r.sink.Store(&errorSink{fn: fn})
}

func (r *Registry) report(he *HandlerError) {
	s := r.sink.Load()
	if s == nil || s.fn == nil {
		return
	}
	s.fn(he)
}

// AddCleanup registers a function to run on Clear. The returned remove is
// idempotent.
func (r *Registry) AddCleanup(fn func()) (remove func()) {
	if r == nil || fn == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.cleanups = append(r.cleanups, cleanup{id: id, fn: fn})
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			for i := range r.cleanups {
				if r.cleanups[i].id == id {
					r.cleanups = append(r.cleanups[:i], r.cleanups[i+1:]...)
					break
				}
			}
			r.mu.Unlock()
		})
	}
}

// Clear drops every handler and runs the registered cleanups in registration
// order.
func (r *Registry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.handlers.Store(&table{byKind: map[Kind][]entry{}})
	pending := r.cleanups
	r.cleanups = nil
	r.mu.Unlock()

	// Outside the lock so a cleanup may touch the registry itself.
	for _, c := range pending {
		c.fn()
	}
}

func (r *Registry) add(kind Kind, source string, fn any) func() {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	next := r.cloneLocked()
	prev := next.byKind[kind]
	list := make([]entry, len(prev), len(prev)+1)
	copy(list, prev)
	next.byKind[kind] = append(list, entry{id: id, source: source, fn: fn})
	r.handlers.Store(next)
	r.mu.Unlock()

	var once sync.Once
	return func() { once.Do(func() { r.remove(kind, id) }) }
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

// On registers fn for this point and returns an idempotent unsubscribe that is
// safe to call from inside a dispatch. source is mandatory: an unattributable
// handler cannot be reported when it fails, so an empty source (or a nil fn) is
// a programming error and panics. A nil registry is not — hooks are optional
// wiring, so registration on one is a no-op.
func (p Point[E, R]) On(r *Registry, source string, fn func(context.Context, E) (R, error)) (unsubscribe func()) {
	if source == "" {
		panic("hooks: On requires a non-empty source for " + string(p.Kind))
	}
	if fn == nil {
		panic("hooks: On requires a non-nil handler for " + string(p.Kind))
	}
	if r == nil {
		return func() {}
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

	// entries is an immutable snapshot: handlers may subscribe or unsubscribe
	// during dispatch, and this round still sees the set it started with.
	for _, e := range entries {
		fn, ok := e.fn.(func(context.Context, E) (R, error))
		if !ok {
			he := &HandlerError{Source: e.source, Kind: p.Kind, Err: errTypeMismatch}
			r.report(he)
			errs = append(errs, he)
			if p.OnError == FailClosed {
				return acc, errors.Join(errs...)
			}
			continue
		}
		out, err := fn(ctx, ev)
		if err != nil {
			he := &HandlerError{Source: e.source, Kind: p.Kind, Err: err}
			r.report(he)
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
