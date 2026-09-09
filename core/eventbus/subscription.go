package eventbus

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrOverflow = errors.New("eventbus: subscriber capacity exceeded")

// SubscribeOptions bounds pending events, including the executing handler.
// Filter, Size and Clone run on the producer and must be fast. Clone transfers
// ownership before Emit returns. Other subscribers must treat events as
// immutable. OnError and OnDrop run on the worker, never on the producer.
// Callbacks must not wait on this subscription's Done or Close. Filter, Size
// and Clone must not reenter the subscription (including Err/Cancel).
type SubscribeOptions[T any] struct {
	Filter   func(T) bool
	Buffer   int
	MaxBytes int64
	Size     func(T) int64
	Clone    func(T) T
	OnError  func(error)
	OnDrop   func(uint64)
}

type queued[T any] struct {
	value T
	bytes int64
}

// Subscription owns a serial worker. Cancel discards queued work; an admitted
// handler may finish. Close drains admitted work. Blocking handlers must
// observe their own cancellation context; the bus cannot interrupt user code.
type Subscription[T any] struct {
	mu                   sync.Mutex
	wake                 *sync.Cond
	queue                []queued[T]
	head, count, pending int
	bytes                int64
	closing              bool
	err                  error
	dropped              uint64
	done                 chan struct{}
	stopped              chan struct{}
	unsub                func()
	opts                 SubscribeOptions[T]
	handler              func(T) error
}

func (b *Bus[T]) SubscribeAsync(opts SubscribeOptions[T], handler func(T) error) (*Subscription[T], error) {
	if b == nil || handler == nil {
		return nil, errors.New("eventbus: bus and handler are required")
	}
	if opts.MaxBytes < 0 || (opts.MaxBytes > 0 && opts.Size == nil) {
		return nil, errors.New("eventbus: byte budget requires a size function and nonnegative limit")
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 256
	}
	s := &Subscription[T]{opts: opts, handler: handler, done: make(chan struct{}), stopped: make(chan struct{}), queue: make([]queued[T], opts.Buffer)}
	s.wake = sync.NewCond(&s.mu)
	s.mu.Lock()
	s.unsub = b.Subscribe(s.enqueue)
	s.mu.Unlock()
	go s.run()
	return s, nil
}

func protect(fn func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("eventbus: subscriber panic: %v", value)
		}
	}()
	return fn()
}

func (s *Subscription[T]) enqueue(event T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	err := protect(func() error {
		if s.opts.Filter != nil && !s.opts.Filter(event) {
			return nil
		}
		var size int64
		if s.opts.Size != nil {
			size = s.opts.Size(event)
		}
		if size < 0 {
			return errors.New("eventbus: negative event size")
		}
		if s.pending >= len(s.queue) || (s.opts.MaxBytes > 0 && size > s.opts.MaxBytes-s.bytes) {
			s.dropped++
			return ErrOverflow
		}
		if s.opts.Clone != nil {
			event = s.opts.Clone(event)
		}
		s.queue[(s.head+s.count)%len(s.queue)] = queued[T]{event, size}
		s.count++
		s.pending++
		s.bytes += size
		s.wake.Signal()
		return nil
	})
	if err != nil {
		s.abortLocked(err)
	}
}

func (s *Subscription[T]) abortLocked(err error) {
	if !s.closing {
		close(s.stopped)
	}
	s.closing = true
	if s.err == nil {
		s.err = err
	}
	s.dropped += uint64(s.count)
	for s.count > 0 {
		s.bytes -= s.queue[s.head].bytes
		s.queue[s.head] = queued[T]{}
		s.head = (s.head + 1) % len(s.queue)
		s.count--
		s.pending--
	}
	s.wake.Broadcast()
}

func (s *Subscription[T]) run() {
	defer close(s.done)
	defer func() {
		s.unsub()
		s.mu.Lock()
		err, dropped := s.err, s.dropped
		s.queue = nil
		s.mu.Unlock()
		if dropped > 0 && s.opts.OnDrop != nil {
			_ = protect(func() error { s.opts.OnDrop(dropped); return nil })
		}
		if err != nil && s.opts.OnError != nil {
			_ = protect(func() error { s.opts.OnError(err); return nil })
		}
	}()
	for {
		s.mu.Lock()
		for s.count == 0 && !s.closing {
			s.wake.Wait()
		}
		if s.count == 0 {
			s.mu.Unlock()
			return
		}
		event := s.queue[s.head]
		s.queue[s.head] = queued[T]{}
		s.head = (s.head + 1) % len(s.queue)
		s.count--
		s.mu.Unlock()
		err := protect(func() error { return s.handler(event.value) })
		s.mu.Lock()
		s.pending--
		s.bytes -= event.bytes
		if err != nil {
			s.abortLocked(err)
		}
		s.mu.Unlock()
	}
}

func (s *Subscription[T]) Cancel() {
	s.mu.Lock()
	s.abortLocked(nil)
	s.mu.Unlock()
	s.unsub()
}

// Close stops admission and waits for admitted work and callbacks to finish.
func (s *Subscription[T]) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closing {
		close(s.stopped)
	}
	s.closing = true
	s.wake.Broadcast()
	s.mu.Unlock()
	s.unsub()
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Subscription[T]) Done() <-chan struct{} { return s.done }

// Stopped closes as soon as admission stops, even if the handler is blocked.
func (s *Subscription[T]) Stopped() <-chan struct{} { return s.stopped }
func (s *Subscription[T]) Err() error               { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
