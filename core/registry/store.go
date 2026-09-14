// Package registry provides the sealed named registry shared by concrete
// capability runtimes. It owns publication state and execution admission, but
// knows nothing about tools, commands, dependency injection, or scope trees.
package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// Value is one immutable named contribution.
type Value[T any] struct {
	Name  string
	Value T
}

// Entry is a published value with its registration metadata.
type Entry[T any] struct {
	Source string
	Name   string
	Group  string
	Value  T
}

// Store is a fixed-composition named registry. Register is allowed only before
// Activate. Close rejects new acquisitions, cancels accepted calls, and waits
// for every acquired lease to be released before discarding declarations.
type Store[T any] struct {
	mu       sync.Mutex
	entries  map[string]Entry[T]
	order    []string
	groups   map[string][]string
	state    state
	inflight int
	nextCall uint64
	calls    map[uint64]context.CancelFunc
	done     chan struct{}
}

func New[T any]() *Store[T] {
	return &Store[T]{
		entries: make(map[string]Entry[T]),
		groups:  make(map[string][]string),
		calls:   make(map[uint64]context.CancelFunc),
		done:    make(chan struct{}),
	}
}

// Register atomically adds one batch. The optional returned function retracts
// that batch before activation. Fixed Profile registries discard this handle
// and retain declarations until the whole registry closes or is discarded.
func (s *Store[T]) Register(source, group string, values ...Value[T]) (func(), error) {
	if s == nil || strings.TrimSpace(source) == "" || len(values) == 0 {
		return nil, ErrInvalid
	}
	names := make([]string, 0, len(values))
	pending := make(map[string]T, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" || name != value.Name {
			return nil, ErrInvalid
		}
		if _, exists := pending[name]; exists {
			return nil, fmt.Errorf("%w: %s (source %s repeated in batch)", ErrDuplicate, name, source)
		}
		pending[name] = value.Value
		names = append(names, name)
	}

	s.mu.Lock()
	if s.state != collecting {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	for _, name := range names {
		if previous, exists := s.entries[name]; exists {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: %s (sources %s and %s)", ErrDuplicate, name, previous.Source, source)
		}
	}
	for _, name := range names {
		s.entries[name] = Entry[T]{Source: source, Name: name, Group: group, Value: pending[name]}
		s.order = append(s.order, name)
		if group != "" {
			s.groups[group] = append(s.groups[group], name)
		}
	}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { s.retract(names) })
	}, nil
}

// Activate seals registration and publishes the collected values.
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

// Get returns one published entry.
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
	return entry, exists
}

// Entries returns the published entries in registration order.
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
			result = append(result, entry)
		}
	}
	return result
}

func (s *Store[T]) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != active {
		return nil
	}
	return append([]string(nil), s.order...)
}

func (s *Store[T]) GroupNames(group string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != active {
		return nil
	}
	return append([]string(nil), s.groups[group]...)
}

// Acquire admits one execution and returns a context canceled when either the
// caller or registry stops. release is idempotent and must be called.
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
	if !exists {
		s.mu.Unlock()
		return zero, nil, nil, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	s.inflight++
	call, cancel := context.WithCancel(ctx)
	s.nextCall++
	callID := s.nextCall
	s.calls[callID] = cancel
	s.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			delete(s.calls, callID)
			s.inflight--
			if s.state == draining && s.inflight == 0 {
				close(s.done)
			}
			s.mu.Unlock()
		})
	}
	return entry, call, release, nil
}

// Close seals admission, cancels accepted calls, and drains them. A timeout
// leaves the store draining so a later Close can finish safely.
func (s *Store[T]) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	switch s.state {
	case closed:
		s.mu.Unlock()
		return nil
	case collecting, active:
		s.state = draining
		if s.inflight == 0 {
			close(s.done)
		}
	}
	cancels := make([]context.CancelFunc, 0, len(s.calls))
	for _, cancel := range s.calls {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}

	select {
	case <-s.done:
		s.mu.Lock()
		s.state = closed
		s.entries = nil
		s.order = nil
		s.groups = nil
		s.calls = nil
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store[T]) retract(names []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == active {
		// Active registries are immutable. Dependency ordering closes the
		// registry before contributor scopes are stopped.
		return
	}
	removed := make(map[string]bool, len(names))
	for _, name := range names {
		if _, exists := s.entries[name]; exists {
			delete(s.entries, name)
			removed[name] = true
		}
	}
	if len(removed) == 0 {
		return
	}
	order := s.order[:0]
	for _, name := range s.order {
		if !removed[name] {
			order = append(order, name)
		}
	}
	s.order = order
	for group, names := range s.groups {
		kept := names[:0]
		for _, name := range names {
			if !removed[name] {
				kept = append(kept, name)
			}
		}
		if len(kept) == 0 {
			delete(s.groups, group)
		} else {
			s.groups[group] = kept
		}
	}
}
