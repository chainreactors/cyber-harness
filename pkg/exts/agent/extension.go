// Package agent installs a generic Agent loop behind extension lifecycle
// admission and drain.
package agent

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
)

var ErrUnavailable = errors.New("agent extension is not active")

type Runtime struct {
	loop     agent.Loop
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// Extension owns one loop installation.
type Extension struct{ runtime *Runtime }

func New(loop agent.Loop) *Extension {
	return &Extension{runtime: &Runtime{loop: loop, done: make(chan struct{})}}
}

// Runtime lends business operations, never ownership of this installation.
func (e *Extension) Runtime() *Runtime {
	if e == nil {
		return nil
	}
	return e.runtime
}
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.runtime == nil || scope == nil {
		return ErrUnavailable
	}
	return e.runtime.load(scope)
}

func (r *Runtime) load(scope *extension.Scope) error {
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

func (rt *Runtime) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	if rt == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rt.mu.Lock()
	if rt.lifetime == nil || rt.stopping || rt.lifetime.Err() != nil {
		rt.mu.Unlock()
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		rt.mu.Unlock()
		return nil, err
	}
	rt.active++
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(rt.lifetime, cancel)
	rt.mu.Unlock()
	defer func() {
		stop()
		cancel()
		rt.mu.Lock()
		rt.active--
		if rt.stopping && rt.active == 0 {
			close(rt.done)
		}
		rt.mu.Unlock()
	}()
	// Derived agent configs must retain the same lifecycle admission boundary.
	config.Loop = rt
	return rt.loop.Run(call, config)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	return e.runtime.close(ctx)
}

func (r *Runtime) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return
	}
	r.stopping = true
	if r.cancel != nil {
		r.cancel()
	}
	if r.active == 0 {
		close(r.done)
	}
}
func (r *Runtime) close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.stop()
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
