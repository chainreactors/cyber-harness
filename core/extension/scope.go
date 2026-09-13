package extension

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Scope belongs to exactly one Extension instance in a Set. Initialization
// and ongoing work have separate cancellation signals. There is no service
// lookup, event dispatch, or business scope hierarchy in this type.
type Scope struct {
	init     context.Context
	lifetime context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	effects  []*effect
	stopped  bool
	stopOnce sync.Once
	stopErr  error
}

type effect struct {
	once    sync.Once
	dispose func()
	err     error
}

func (e *effect) stop() error {
	e.once.Do(func() {
		defer func() {
			e.dispose = nil
			if p := recover(); p != nil {
				e.err = fmt.Errorf("registration disposal panicked: %v", p)
			}
		}()
		e.dispose()
	})
	return e.err
}

func newScope(init context.Context) *Scope {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Scope{init: init, lifetime: lifetime, cancel: cancel}
}

// Init bounds Load only. Do not retain it for background work.
func (s *Scope) Init() context.Context { return s.init }

// Lifetime is canceled when the owning Set begins closing this extension.
func (s *Scope) Lifetime() context.Context { return s.lifetime }

// Track owns a synchronous registration revocation. Callbacks run once in
// reverse order before Lifetime is canceled. They must not wait for work or
// call their owning Set's lifecycle. On error ownership has not transferred.
func (s *Scope) Track(dispose func()) error {
	if s == nil || dispose == nil {
		return errors.New("scope and dispose are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("extension is stopping")
	}
	e := &effect{dispose: dispose}
	s.effects = append(s.effects, e)
	return nil
}

// stop is called only while Set's lifecycle gate is held. It seals registration
// before invoking callbacks and never invokes user code under s.mu.
func (s *Scope) stop() error {
	if s == nil {
		return nil
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		effects := s.effects
		s.effects = nil
		s.mu.Unlock()
		var errs []error
		for i := len(effects) - 1; i >= 0; i-- {
			errs = append(errs, effects[i].stop())
		}
		s.cancel()
		s.stopErr = errors.Join(errs...)
	})
	return s.stopErr
}
