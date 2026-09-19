// Package registry provides the concurrent named store used by concrete
// resource Points. It owns publication and per-batch execution draining.
package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/resource"
)

var (
	ErrInvalid     = errors.New("invalid registry entry")
	ErrDuplicate   = errors.New("duplicate registry entry")
	ErrUnknown     = errors.New("unknown registry entry")
	ErrUnavailable = errors.New("registry is unavailable")
)

type state uint8

const (
	collecting state = iota
	active
	draining
	closed
)

type Value[T any] struct {
	Name  string
	Value T
}

type Entry[T any] struct {
	Name  string
	Value T
	batch *batch
}

type batch struct {
	names    []string
	closing  bool
	inflight int
	calls    map[*call]struct{}
	done     chan struct{}
}

type call struct{ cancel context.CancelFunc }

type Store[T any] struct {
	mu      sync.Mutex
	entries map[string]Entry[T]
	order   []string
	state   state
	batches map[*batch]struct{}
}

func New[T any]() *Store[T] {
	return &Store[T]{
		entries: make(map[string]Entry[T]),
		batches: make(map[*batch]struct{}),
	}
}

// Add atomically publishes one batch. It is valid both before and after
// activation; reads remain hidden until Activate.
func (s *Store[T]) Add(values ...Value[T]) (resource.Handle, error) {
	if s == nil || len(values) == 0 {
		return nil, ErrInvalid
	}
	names := make([]string, 0, len(values))
	pending := make(map[string]Value[T], len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" || name != value.Name {
			return nil, ErrInvalid
		}
		if _, exists := pending[name]; exists {
			return nil, fmt.Errorf("%w: %s repeated in batch", ErrDuplicate, name)
		}
		pending[name] = value
		names = append(names, name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == draining || s.state == closed {
		return nil, ErrUnavailable
	}
	for _, name := range names {
		if _, exists := s.entries[name]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicate, name)
		}
	}
	b := &batch{names: names, calls: make(map[*call]struct{}), done: make(chan struct{})}
	s.batches[b] = struct{}{}
	for _, name := range names {
		value := pending[name]
		s.entries[name] = Entry[T]{Name: name, Value: value.Value, batch: b}
		s.order = append(s.order, name)
	}
	return &batchHandle[T]{store: s, batch: b}, nil
}

func (s *Store[T]) Activate(ctx context.Context) error {
	if s == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != collecting {
		return ErrUnavailable
	}
	s.state = active
	return nil
}

func (s *Store[T]) Get(name string) (Entry[T], bool) {
	var zero Entry[T]
	if s == nil {
		return zero, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != active {
		return zero, false
	}
	entry, exists := s.entries[name]
	entry.batch = nil
	return entry, exists
}

func (s *Store[T]) Entries() []Entry[T] {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != active {
		return nil
	}
	result := make([]Entry[T], 0, len(s.order))
	for _, name := range s.order {
		if entry, exists := s.entries[name]; exists {
			entry.batch = nil
			result = append(result, entry)
		}
	}
	return result
}

func (s *Store[T]) Names() []string {
	entries := s.Entries()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func (s *Store[T]) Acquire(ctx context.Context, name string) (Entry[T], context.Context, func(), error) {
	var zero Entry[T]
	if s == nil {
		return zero, nil, nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return zero, nil, nil, err
	}
	if s.state != active {
		s.mu.Unlock()
		return zero, nil, nil, ErrUnavailable
	}
	entry, exists := s.entries[name]
	if !exists || entry.batch.closing {
		s.mu.Unlock()
		return zero, nil, nil, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	b := entry.batch
	b.inflight++
	callContext, cancel := context.WithCancel(ctx)
	activeCall := &call{cancel: cancel}
	b.calls[activeCall] = struct{}{}
	entry.batch = nil
	s.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			delete(b.calls, activeCall)
			b.inflight--
			if b.closing && b.inflight == 0 {
				close(b.done)
			}
			s.mu.Unlock()
		})
	}
	return entry, callContext, release, nil
}

func (s *Store[T]) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.state == closed {
		s.mu.Unlock()
		return nil
	}
	s.state = draining
	batches := make([]*batch, 0, len(s.batches))
	for b := range s.batches {
		s.beginCloseLocked(b)
		batches = append(batches, b)
	}
	s.mu.Unlock()
	for _, b := range batches {
		if err := waitBatch(ctx, b); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.state = closed
	s.entries, s.order, s.batches = nil, nil, nil
	s.mu.Unlock()
	return nil
}

type batchHandle[T any] struct {
	mu     sync.Mutex
	store  *Store[T]
	batch  *batch
	closed bool
}

func (h *batchHandle[T]) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.store.mu.Lock()
	h.store.beginCloseLocked(h.batch)
	h.store.mu.Unlock()
	if err := waitBatch(ctx, h.batch); err != nil {
		return err
	}
	h.store.mu.Lock()
	delete(h.store.batches, h.batch)
	h.store.mu.Unlock()
	h.closed = true
	return nil
}

func (s *Store[T]) beginCloseLocked(b *batch) {
	if b.closing {
		return
	}
	b.closing = true
	removed := make(map[string]bool, len(b.names))
	for _, name := range b.names {
		entry, exists := s.entries[name]
		if exists && entry.batch == b {
			delete(s.entries, name)
			removed[name] = true
		}
	}
	order := s.order[:0]
	for _, name := range s.order {
		if !removed[name] {
			order = append(order, name)
		}
	}
	s.order = order
	for activeCall := range b.calls {
		activeCall.cancel()
	}
	if b.inflight == 0 {
		close(b.done)
	}
}

func waitBatch(ctx context.Context, b *batch) error {
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return errors.Join(resource.ErrCloseIncomplete, ctx.Err())
	}
}
