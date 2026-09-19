// Package resource connects extensions by type, without assigning string names
// to resource types. It carries both directions of that wiring:
//
//   - Define and Add are many-to-one. One extension declares the point that
//     owns values of type T; others contribute them. Concrete points own all
//     domain semantics.
//   - Provide and Use are one-to-many. One extension offers the single value of
//     type T; others borrow it.
//
// A type is one or the other, never both, because the two are keyed
// differently: a point is keyed by the type of the value it stores
// (commands.Command), a capability by the type describing behavior
// (commands.Executor). An element type and a behavior interface can never be
// the same named type, so the rule holds by construction.
//
// A capability key must be a named type declared for the purpose and owned by
// the provider's package. Never key on a builtin, on a structural func, map or
// slice type, or on a type from a third-party module: a global key that anyone
// can name is a collision waiting to happen. A consumer that wants a narrower
// view declares its own interface locally and converts -- Go interfaces are
// structural, and narrowing is the consumer's private concern, not a second
// coordination point.
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

// kind tells a point from a capability so that a mismatch reports which one
// the type actually is instead of an opaque cast failure.
type kind uint8

const (
	kindPoint kind = iota
	kindValue
)

func (k kind) String() string {
	if k == kindValue {
		return "a capability"
	}
	return "a contribution point"
}

type definition struct {
	kind      kind
	value     any // Point[T] when kindPoint, T when kindValue
	definedAt int
	// dependents counts outstanding contribution batches and borrows. The
	// declaring extension cannot close while any remain.
	dependents int
	closing    bool
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
	if isNil(point) {
		return nil, ErrInvalid
	}
	return declare[T](r, kindPoint, point)
}

// Provide registers the single value of type T so that other extensions can
// Use it. Always write the type argument explicitly -- Provide(r, registry)
// infers the concrete type and silently registers the wrong key, which only
// surfaces when a consumer's Use fails at load.
func Provide[T any](r *Registry, value T) (Handle, error) {
	if isNil(value) {
		return nil, ErrInvalid
	}
	return declare[T](r, kindValue, value)
}

func declare[T any](r *Registry, k kind, value any) (Handle, error) {
	if r == nil || r.shared == nil {
		return nil, ErrInvalid
	}
	typ := reflect.TypeFor[T]()
	r.shared.mu.Lock()
	defer r.shared.mu.Unlock()
	if r.shared.frozen {
		return nil, ErrDefinitionsFrozen
	}
	if existing, exists := r.shared.types[typ]; exists {
		return nil, fmt.Errorf("%w: %s is already %s", ErrTypeDefined, typ, existing.kind)
	}
	d := &definition{kind: k, value: value, definedAt: r.position}
	r.shared.types[typ] = d
	return &definitionHandle{registry: r.shared, typ: typ, definition: d}, nil
}

func Add[T any](r *Registry, values ...T) (Handle, error) {
	if r == nil || r.shared == nil || len(values) == 0 {
		return nil, ErrInvalid
	}
	typ := reflect.TypeFor[T]()
	r.shared.mu.Lock()
	d, err := resolveLocked(r, typ, kindPoint)
	if err != nil {
		r.shared.mu.Unlock()
		return nil, err
	}
	point, ok := d.value.(Point[T])
	if !ok {
		r.shared.mu.Unlock()
		return nil, fmt.Errorf("%w: inconsistent point for %s", ErrInvalid, typ)
	}
	d.dependents++
	r.shared.mu.Unlock()
	committed := false
	defer func() {
		if committed {
			return
		}
		r.shared.mu.Lock()
		d.dependents--
		r.shared.mu.Unlock()
	}()

	handle, err := point.Add(values...)
	if err != nil || handle == nil {
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalid
	}
	committed = true
	return &contributionHandle{registry: r.shared, definition: d, inner: handle}, nil
}

// Use borrows the value of type T. The returned Handle releases the borrow; the
// provider cannot close while any borrow is outstanding.
//
// Use is total: a successful load means the capability is there. It reports an
// error only for a wiring fault -- nothing provided T, the provider is ordered
// after this caller, or the provider is already closing. Optional capabilities
// are expressed by leaving an extension out of the set, or by providing a null
// implementation, never by tolerating a missing one here.
//
// Use is refused once the registry is frozen, which confines it to load.
func Use[T any](r *Registry) (T, Handle, error) {
	var zero T
	if r == nil || r.shared == nil {
		return zero, nil, ErrInvalid
	}
	typ := reflect.TypeFor[T]()
	r.shared.mu.Lock()
	defer r.shared.mu.Unlock()
	if r.shared.frozen {
		return zero, nil, fmt.Errorf("%w: %s is only available while loading", ErrDefinitionsFrozen, typ)
	}
	d, err := resolveLocked(r, typ, kindValue)
	if err != nil {
		return zero, nil, err
	}
	// Self-use would deadlock teardown: the provision closes before this
	// extension's Close, while the borrow releases after it.
	if d.definedAt == r.position {
		return zero, nil, fmt.Errorf("%w: %s is provided by this extension", ErrInvalid, typ)
	}
	value, ok := d.value.(T)
	if !ok {
		return zero, nil, fmt.Errorf("%w: inconsistent capability for %s", ErrInvalid, typ)
	}
	d.dependents++
	return value, &borrowHandle{registry: r.shared, definition: d}, nil
}

// resolveLocked finds a declared type visible to a caller at r.position. The
// caller holds r.shared.mu.
func resolveLocked(r *Registry, typ reflect.Type, want kind) (*definition, error) {
	d := r.shared.types[typ]
	if d == nil || d.closing || d.definedAt > r.position {
		return nil, fmt.Errorf("%w: %s", ErrTypeUnknown, typ)
	}
	if d.kind != want {
		return nil, fmt.Errorf("%w: %s is %s, not %s", ErrInvalid, typ, d.kind, want)
	}
	return d, nil
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
	if d.dependents != 0 {
		h.registry.mu.Unlock()
		return fmt.Errorf("%w: %s still has %d dependents", ErrCloseIncomplete, h.typ, d.dependents)
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
	h.definition.dependents--
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

// borrowHandle releases one borrow. It is separate from contributionHandle
// because a borrow installs nothing: there is no inner handle to unwind, only
// the reference the provider is waiting on.
type borrowHandle struct {
	mu         sync.Mutex
	registry   *shared
	definition *definition
	closed     bool
}

func (h *borrowHandle) Close(context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.registry.mu.Lock()
	h.definition.dependents--
	h.registry.mu.Unlock()
	h.closed = true
	return nil
}
