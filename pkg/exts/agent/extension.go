// Package agent installs the generic harness loop and optional session host.
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

type loopRuntime struct {
	loop     agent.Loop
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// Extension owns loop admission and session shutdown as one installation.
type Extension struct{ runtime *Runtime }

type Config struct{ Loop agent.Loop }
type Runtime struct{ loop *loopRuntime }

func (e *Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "agent-loop", Description: "agent loop provider", Provides: []extension.Service{extension.ServiceOf[agent.Loop]("agent.loop")}}
}

func New(config Config) (*Extension, error) {
	return &Extension{runtime: &Runtime{loop: &loopRuntime{loop: config.Loop, done: make(chan struct{})}}}, nil
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
	rt := e.runtime
	if rt.loop != nil {
		if err := rt.loop.load(scope); err != nil {
			return err
		}
	}
	return nil
}

func (r *loopRuntime) load(scope *extension.Scope) error {
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
	if rt == nil || rt.loop == nil {
		return nil, ErrUnavailable
	}
	r := rt.loop
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
	config.Loop = rt
	return r.loop.Run(call, config)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	rt := e.runtime
	// Seal loop admission before waiting for any session or direct loop caller.
	if rt.loop != nil {
		rt.loop.stop()
	}
	var err error
	if rt.loop != nil {
		err = errors.Join(err, rt.loop.close(ctx))
	}
	return err
}

func (r *loopRuntime) stop() {
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
func (r *loopRuntime) close(ctx context.Context) error {
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
