// Package resource connects typed contribution points without assigning
// string names to resource types. Concrete points own all domain semantics.
package resource

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
)

var (
	ErrInvalid           = errors.New("invalid resource registration")
	ErrTypeDefined       = errors.New("resource type already defined")
	ErrTypeUnknown       = errors.New("resource type is not defined")
	ErrDefinitionsFrozen = errors.New("resource type definitions are frozen")
	ErrCloseIncomplete   = errors.New("resource cleanup incomplete")
)

// Handle retracts one definition or one atomic contribution batch. Close is
// idempotent. ErrCloseIncomplete is the only error that requires a retry.
type Handle interface {
	Close(context.Context) error
}

type HandleFunc func(context.Context) error

func (f HandleFunc) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	return f(ctx)
}

// Point accepts values of one resource type. Duplicate and replacement rules
// belong to the domain implementation, not to Registry.
type Point[T any] interface {
	Add(...T) (Handle, error)
}

type definition struct {
	point         any
	definedAt     int
	contributions int
	closing       bool
}

type shared struct {
	mu     sync.Mutex
	types  map[reflect.Type]*definition
	frozen bool
}

// Registry is a lightweight view over one typed resource namespace. Scoped
// views carry only a load ordinal, which prevents an earlier Extension from
// retaining its Scope and contributing to a type defined later.
type Registry struct {
	shared   *shared
	position int
}

func New() *Registry {
	return &Registry{shared: &shared{types: make(map[reflect.Type]*definition)}, position: math.MaxInt}
}

// Scope returns a view associated with one Extension's declaration order.
func (r *Registry) Scope(position int) *Registry {
	if r == nil {
		return nil
	}
	return &Registry{shared: r.shared, position: position}
}

// Freeze prevents new resource types after composition. Existing Points stay
// mutable and may continue to accept hot contributions.
func (r *Registry) Freeze() {
	if r == nil || r.shared == nil {
		return
	}
	r.shared.mu.Lock()
	r.shared.frozen = true
	r.shared.mu.Unlock()
}

func Define[T any](r *Registry, point Point[T]) (Handle, error) {
	if r == nil || r.shared == nil || isNil(point) {
		return nil, ErrInvalid
	}
	typ := reflect.TypeFor[T]()
	r.shared.mu.Lock()
	defer r.shared.mu.Unlock()
	if r.shared.frozen {
		return nil, ErrDefinitionsFrozen
	}
	if _, exists := r.shared.types[typ]; exists {
		return nil, fmt.Errorf("%w: %s", ErrTypeDefined, typ)
	}
	d := &definition{point: point, definedAt: r.position}
	r.shared.types[typ] = d
	return &definitionHandle{registry: r.shared, typ: typ, definition: d}, nil
}

func Add[T any](r *Registry, values ...T) (Handle, error) {
	if r == nil || r.shared == nil || len(values) == 0 {
		return nil, ErrInvalid
	}
	typ := reflect.TypeFor[T]()
	r.shared.mu.Lock()
	d := r.shared.types[typ]
	if d == nil {
		r.shared.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrTypeUnknown, typ)
	}
	if d.closing || d.definedAt > r.position {
		r.shared.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrTypeUnknown, typ)
	}
	point, ok := d.point.(Point[T])
	if !ok {
		r.shared.mu.Unlock()
		return nil, fmt.Errorf("%w: inconsistent point for %s", ErrInvalid, typ)
	}
	d.contributions++
	r.shared.mu.Unlock()

	handle, err := point.Add(values...)
	if err != nil || handle == nil {
		r.shared.mu.Lock()
		d.contributions--
		r.shared.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalid
	}
	return &contributionHandle{registry: r.shared, definition: d, inner: handle}, nil
}

type definitionHandle struct {
	mu         sync.Mutex
	registry   *shared
	typ        reflect.Type
	definition *definition
	closed     bool
}

func (h *definitionHandle) Close(context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.registry.mu.Lock()
	d := h.registry.types[h.typ]
	if d != h.definition {
		h.registry.mu.Unlock()
		h.closed = true
		return nil
	}
	d.closing = true
	if d.contributions != 0 {
		h.registry.mu.Unlock()
		return fmt.Errorf("%w: %s still has %d contribution batches", ErrCloseIncomplete, h.typ, d.contributions)
	}
	delete(h.registry.types, h.typ)
	h.registry.mu.Unlock()
	h.closed = true
	return nil
}

type contributionHandle struct {
	mu         sync.Mutex
	registry   *shared
	definition *definition
	inner      Handle
	closed     bool
}

func (h *contributionHandle) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	err := h.inner.Close(ctx)
	if errors.Is(err, ErrCloseIncomplete) {
		return err
	}
	h.registry.mu.Lock()
	h.definition.contributions--
	h.registry.mu.Unlock()
	h.closed = true
	return err
}

func isNil(value any) bool {
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
