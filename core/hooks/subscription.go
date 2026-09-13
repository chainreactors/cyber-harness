package hooks

import (
	"context"
	"sync"
)

// Subscription owns admission to one handler, not its business resources.
// Cancel is safe inside a handler. Close must be called outside that handler.
type Subscription struct {
	mu      sync.Mutex
	stopped bool
	active  int
	done    chan struct{}
	remove  func()
}

func (s *Subscription) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.active++
	return true
}

func (s *Subscription) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.stopped && s.active == 0 {
		close(s.done)
	}
}

func (s *Subscription) Cancel() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.active == 0 {
		close(s.done)
	}
	remove := s.remove
	s.remove = nil
	s.mu.Unlock()
	if remove != nil {
		remove()
	}
}

func (s *Subscription) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.Cancel()
	if ctx == nil {
		ctx = context.Background()
	}
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
