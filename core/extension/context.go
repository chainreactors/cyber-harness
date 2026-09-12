package extension

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Dispose retracts registration without waiting for accepted work. Resources
// and draining belong to Extension.Close, after registration has been stopped.
type Dispose func()

var ownerSequence atomic.Uint64

// Context belongs to exactly one Extension instance in a Set. Initialization
// and ongoing work have separate cancellation signals. There is no service
// lookup, event dispatch, or business scope hierarchy in this type.
type Context struct {
	init     context.Context
	lifetime context.Context
	cancel   context.CancelFunc
	owner    string
	mu       sync.Mutex
	effects  []*effect
	stopped  bool
	stopOnce sync.Once
	stopErr  error
}

type effect struct {
	once    sync.Once
	dispose Dispose
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

func newContext(init context.Context, id string) *Context {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Context{
		init: init, lifetime: lifetime, cancel: cancel,
		owner: fmt.Sprintf("%s/%d", id, ownerSequence.Add(1)),
	}
}

// Init bounds Load only. Do not retain it for background work.
func (c *Context) Init() context.Context { return c.init }

// Lifetime is canceled when the owning Set begins closing this extension.
func (c *Context) Lifetime() context.Context { return c.lifetime }

// Owner is unique to this instance, including across Sets with the same IDs.
func (c *Context) Owner() string { return c.owner }

// Track owns an idempotent synchronous revocation. The returned function
// retracts the registration immediately; it does not merely untrack cleanup.
// Callbacks must not wait for work or call their owning Set's lifecycle.
// On error ownership has not transferred: the caller must undo registration.
func (c *Context) Track(dispose Dispose) (Dispose, error) {
	if c == nil || dispose == nil {
		return nil, errors.New("context and dispose are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return nil, errors.New("extension is stopping")
	}
	e := &effect{dispose: dispose}
	c.effects = append(c.effects, e)
	return func() { _ = e.stop() }, nil
}

// stop is called only while Set's lifecycle gate is held. It seals registration
// before invoking callbacks and never invokes user code under c.mu.
func (c *Context) stop() error {
	if c == nil {
		return nil
	}
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.stopped = true
		effects := c.effects
		c.effects = nil
		c.mu.Unlock()
		var errs []error
		for i := len(effects) - 1; i >= 0; i-- {
			errs = append(errs, effects[i].stop())
		}
		c.cancel()
		c.stopErr = errors.Join(errs...)
	})
	return c.stopErr
}
