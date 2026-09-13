// Package agent installs a reasoning loop with an explicitly owned lifetime.
package agent

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/extension"
)

var ErrUnavailable = errors.New("agent extension is not active")

// Runtime is the admitted agent.Loop published to sessions. It intentionally
// has no lifecycle methods.
type Runtime struct {
	loop     agent.Loop
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// Extension is the sole lifecycle owner of Runtime.
type Extension struct {
	runtime *Runtime
}

// New is inert. The profile supplies the algorithm; no default is installed.
func New(loop agent.Loop) *Extension {
	return &Extension{runtime: &Runtime{loop: loop, done: make(chan struct{})}}
}

func (e *Extension) Loop() *Runtime {
	if e == nil {
		return nil
	}
	return e.runtime
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.runtime == nil || scope == nil {
		return ErrUnavailable
	}
	r := e.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return ErrUnavailable
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if r.loop == nil {
		return errors.New("agent extension requires a loop")
	}
	value := reflect.ValueOf(r.loop)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return errors.New("agent extension requires a non-nil loop")
		}
	}
	if r.lifetime == nil {
		r.lifetime, r.cancel = context.WithCancel(scope.Lifetime())
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	if r == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.lifetime == nil || r.stopping || r.lifetime.Err() != nil {
		r.mu.Unlock()
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.active++
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.lifetime, cancel)
	r.mu.Unlock()
	defer func() {
		stop()
		cancel()
		r.mu.Lock()
		r.active--
		if r.stopping && r.active == 0 {
			close(r.done)
		}
		r.mu.Unlock()
	}()
	// Derived agent configs must retain the same lifecycle admission boundary.
	config.Loop = r
	return r.loop.Run(call, config)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	r := e.runtime
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if !r.stopping {
		r.stopping = true
		if r.cancel != nil {
			r.cancel()
		}
		if r.active == 0 {
			close(r.done)
		}
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ extension.Extension = (*Extension)(nil)
var _ agent.Loop = (*Runtime)(nil)
