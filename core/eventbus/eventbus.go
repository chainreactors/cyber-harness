// Package eventbus provides application-owned, typed event subscriptions.
package eventbus

import "sync"

type Bus[T any] struct {
	mu   sync.RWMutex
	subs []*Subscription[T]
}

func New[T any]() *Bus[T] { return &Bus[T]{} }

// Subscribe preserves synchronous delivery. Handlers run outside the bus lock
// and must synchronize their own state when producers emit concurrently.
func (b *Bus[T]) Subscribe(handler func(T)) *Subscription[T] {
	if b == nil || handler == nil {
		panic("eventbus: bus and handler are required")
	}
	idle := make(chan struct{})
	close(idle)
	s := &Subscription[T]{bus: b, syncHandler: handler, done: make(chan struct{}), stopped: make(chan struct{}), idle: idle}
	b.subscribe(s)
	return s
}

// SubscribeFiltered preserves synchronous delivery for consumers that need an
// immediate visibility boundary. Use SubscribeAsync for independently bounded,
// non-blocking consumers.
func (b *Bus[T]) SubscribeFiltered(filter func(T) bool, handler func(T)) *Subscription[T] {
	if handler == nil {
		panic("eventbus: handler is required")
	}
	return b.Subscribe(func(event T) {
		if filter == nil || filter(event) {
			handler(event)
		}
	})
}

func (b *Bus[T]) subscribe(s *Subscription[T]) {
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
}

func (b *Bus[T]) unsubscribe(subscription *Subscription[T]) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == subscription {
			copy(b.subs[i:], b.subs[i+1:])
			b.subs[len(b.subs)-1] = nil
			b.subs = b.subs[:len(b.subs)-1]
			return
		}
	}
}

func (b *Bus[T]) Emit(event T) {
	b.mu.RLock()
	snapshot := append([]*Subscription[T](nil), b.subs...)
	b.mu.RUnlock()
	for _, s := range snapshot {
		if s.wake != nil {
			s.enqueue(event)
		} else {
			s.deliver(event)
		}
	}
}
