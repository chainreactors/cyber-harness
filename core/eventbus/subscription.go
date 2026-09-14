package eventbus

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrOverflow = errors.New("eventbus: subscriber capacity exceeded")

// SubscribeOptions bounds pending events, including the executing handler.
// Filter, Size and Clone are value policies: they run on the producer and must
// be fast. Clone transfers ownership before Emit returns. Processing state is
// reported by Subscription.Err and Subscription.Dropped; the queue never calls
// a second, hidden error callback.
type SubscribeOptions[T any] struct {
	Filter   func(T) bool
	Buffer   int
	MaxBytes int64
	Size     func(T) int64
	Clone    func(T) T
}

type queued[T any] struct {
	value T
	bytes int64
}

// Subscription owns callback admission and completion. Synchronous callbacks
// run on producers; async callbacks run on one owned serial worker. Cancel
// stops admission and discards queued work without waiting. Close stops
// admission and waits for admitted callbacks, draining async queues. Callbacks
// may Cancel themselves but must not wait for their own Done or Close.
// Blocking handlers own their cancellation; the bus cannot interrupt user code.
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
	idle                 chan struct{}
	bus                  *Bus[T]
	syncHandler          func(T)
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
	idle := make(chan struct{})
	close(idle)
	s := &Subscription[T]{bus: b, opts: opts, handler: handler, done: make(chan struct{}), stopped: make(chan struct{}), idle: idle, queue: make([]queued[T], opts.Buffer)}
	s.wake = sync.NewCond(&s.mu)
	b.subscribe(s)
	go s.run()
	return s, nil
}

// deliver checks admission after taking the bus snapshot. An old snapshot
// cannot invoke a stopped subscription. Completion runs even if the handler
// panics; synchronous panics retain their normal propagation to the producer.
func (s *Subscription[T]) deliver(event T) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.beginLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.finishLocked()
		if s.closing && s.pending == 0 {
			close(s.done)
		}
		s.mu.Unlock()
	}()
	s.syncHandler(event)
}

func (s *Subscription[T]) beginLocked() {
	if s.pending == 0 {
		s.idle = make(chan struct{})
	}
	s.pending++
}

func (s *Subscription[T]) finishLocked() {
	s.pending--
	if s.pending == 0 {
		close(s.idle)
	}
}

func (s *Subscription[T]) stopLocked() {
	if !s.closing {
		s.closing = true
		close(s.stopped)
		if s.wake == nil && s.pending == 0 {
			close(s.done)
		}
	}
	if s.wake != nil {
		s.wake.Broadcast()
	}
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
		s.beginLocked()
		s.bytes += size
		s.wake.Signal()
		return nil
	})
	if err != nil {
		s.abortLocked(err)
	}
}

func (s *Subscription[T]) abortLocked(err error) {
	s.stopLocked()
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
	if s.pending == 0 {
		select {
		case <-s.idle:
		default:
			close(s.idle)
		}
	}
}

func (s *Subscription[T]) run() {
	defer close(s.done)
	defer func() {
		s.bus.unsubscribe(s)
		s.mu.Lock()
		s.queue = nil
		s.mu.Unlock()
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
		s.finishLocked()
		s.bytes -= event.bytes
		if err != nil {
			s.abortLocked(err)
		}
		s.mu.Unlock()
	}
}

// Flush waits for work admitted before the call to finish without stopping
// future admission. Producers that continue emitting concurrently may create
// more work after this boundary.
func (s *Subscription[T]) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	default:
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Subscription[T]) Cancel() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.abortLocked(nil)
	s.mu.Unlock()
	s.bus.unsubscribe(s)
}

// Close stops admission and waits for admitted work and callbacks to finish.
// It reports only an incomplete wait. Processing failures remain available via
// Err after completion; resource owners must collect them separately.
func (s *Subscription[T]) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
	s.bus.unsubscribe(s)
	// Completed cleanup succeeds even if the caller's deadline also expired.
	select {
	case <-s.done:
		return nil
	default:
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Subscription[T]) Done() <-chan struct{} { return s.done }

// Stopped closes as soon as admission stops, even if the handler is blocked.
func (s *Subscription[T]) Stopped() <-chan struct{} { return s.stopped }
func (s *Subscription[T]) Err() error               { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// Dropped reports values that were rejected by backpressure or discarded by
// cancellation. It remains available after the subscription has completed.
func (s *Subscription[T]) Dropped() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}
