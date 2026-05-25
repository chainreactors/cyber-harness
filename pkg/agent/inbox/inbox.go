package inbox

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInboxClosed = errors.New("inbox closed")
	ErrInboxFull   = errors.New("inbox full")
)

type Inbox interface {
	Push(msg Message) error
	Drain() []Message
	Close()
	Closed() bool
	Len() int
	Wait(ctx context.Context) bool
	RegisterProducer(name string) *ProducerHandle
	ActiveProducers() int
}

type ProducerHandle struct {
	inbox *Buffered
	name  string
	once  sync.Once
}

func (h *ProducerHandle) Done() {
	h.once.Do(func() { h.inbox.removeProducer(h.name) })
}

type Buffered struct {
	mu        sync.Mutex
	buf       []Message
	capacity  int
	closed    bool
	notify    chan struct{}
	producers map[string]struct{}
}

func NewBuffered(capacity int) *Buffered {
	if capacity <= 0 {
		capacity = 1
	}
	return &Buffered{
		buf:       make([]Message, 0, capacity),
		capacity:  capacity,
		notify:    make(chan struct{}),
		producers: make(map[string]struct{}),
	}
}

func (b *Buffered) Push(msg Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrInboxClosed
	}
	if len(b.buf) >= b.capacity {
		return ErrInboxFull
	}
	wasEmpty := len(b.buf) == 0
	b.buf = append(b.buf, msg)
	if wasEmpty {
		b.wakeLocked()
	}
	return nil
}

func (b *Buffered) RegisterProducer(name string) *ProducerHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.producers[name] = struct{}{}
	b.wakeLocked()
	return &ProducerHandle{inbox: b, name: name}
}

func (b *Buffered) removeProducer(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.producers, name)
	b.wakeLocked()
}

func (b *Buffered) ActiveProducers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.producers)
}

func (b *Buffered) Drain() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) == 0 {
		return nil
	}
	out := b.buf
	b.buf = make([]Message, 0, b.capacity)
	if needsSort(out) {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Priority > out[j].Priority
		})
	}
	return out
}

func needsSort(msgs []Message) bool {
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Priority > msgs[i-1].Priority {
			return true
		}
	}
	return false
}

func (b *Buffered) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.notify != nil {
		close(b.notify)
		b.notify = nil
	}
}

func (b *Buffered) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *Buffered) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

func (b *Buffered) Wait(ctx context.Context) bool {
	for {
		b.mu.Lock()
		if len(b.buf) > 0 {
			b.mu.Unlock()
			return true
		}
		if b.closed {
			b.mu.Unlock()
			return false
		}
		ch := b.notify
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return false
		case <-ch:
		}
	}
}

func (b *Buffered) wakeLocked() {
	if b.notify == nil {
		return
	}
	close(b.notify)
	b.notify = make(chan struct{})
}
