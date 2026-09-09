// Package eventbus provides application-owned, typed event subscriptions.
package eventbus

import "sync"

type entry[T any] struct {
	id      int
	handler func(T)
}

type Bus[T any] struct {
	mu   sync.RWMutex
	subs []entry[T]
	next int
}

func New[T any]() *Bus[T] { return &Bus[T]{} }

// Subscribe preserves synchronous delivery. Handlers run outside the bus lock
// and must synchronize their own state when producers emit concurrently.
func (b *Bus[T]) Subscribe(handler func(T)) func() {
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs = append(b.subs, entry[T]{id: id, handler: handler})
	b.mu.Unlock()
	return func() { b.unsubscribe(id) }
}

// SubscribeFiltered preserves synchronous delivery for consumers that need an
// immediate visibility boundary (for example a journal before a file switch).
// Use SubscribeAsync for independently bounded, non-blocking consumers.
func (b *Bus[T]) SubscribeFiltered(filter func(T) bool, handler func(T)) func() {
	return b.Subscribe(func(event T) {
		if filter == nil || filter(event) {
			handler(event)
		}
	})
}

func (b *Bus[T]) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s.id == id {
			copy(b.subs[i:], b.subs[i+1:])
			b.subs[len(b.subs)-1] = entry[T]{}
			b.subs = b.subs[:len(b.subs)-1]
			return
		}
	}
}

func (b *Bus[T]) Emit(event T) {
	b.mu.RLock()
	snapshot := append([]entry[T](nil), b.subs...)
	b.mu.RUnlock()
	for _, s := range snapshot {
		s.handler(event)
	}
}
