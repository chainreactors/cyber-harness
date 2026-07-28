// Package deps carries optional, typed dependencies from the assembly layer to
// the command factories. Keys are declared by the package that owns the value
// type, so the wiring layer (pkg/commands) never has to import — and therefore
// never links — the packages whose values it forwards.
package deps

import "sync"

// keyID gives a key its identity. Two keys declared with the same name are
// still distinct, and a key cannot be forged from a string.
type keyID struct{ name string }

// Key names a dependency of type T.
type Key[T any] struct{ id *keyID }

func NewKey[T any](name string) Key[T] { return Key[T]{id: &keyID{name: name}} }

// Name reports the declared name, used when logging a missing dependency.
func Name[T any](k Key[T]) string {
	if k.id == nil {
		return "<invalid>"
	}
	return k.id.name
}

// Bag maps key identity to value. Accessors are package functions because Go
// methods cannot declare type parameters.
type Bag struct {
	mu     sync.RWMutex
	values map[*keyID]any
}

func New() *Bag { return &Bag{values: make(map[*keyID]any)} }

// Set stores v under k. A nil Bag is a no-op so a half-built Deps cannot panic;
// use commands.Provide when the bag may not exist yet.
func Set[T any](b *Bag, k Key[T], v T) {
	if b == nil || k.id == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values = make(map[*keyID]any)
	}
	b.values[k.id] = v
}

// Get returns the value stored under k. The key identity already guarantees the
// type; the assertion exists only because the backing map is untyped.
func Get[T any](b *Bag, k Key[T]) (T, bool) {
	var zero T
	if b == nil || k.id == nil {
		return zero, false
	}
	b.mu.RLock()
	raw, stored := b.values[k.id]
	b.mu.RUnlock()
	if !stored {
		return zero, false
	}
	typed, ok := raw.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

func Has[T any](b *Bag, k Key[T]) bool {
	_, ok := Get(b, k)
	return ok
}
