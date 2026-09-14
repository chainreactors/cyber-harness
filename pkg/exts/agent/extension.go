// Package agent owns admission and drain for an agent.Loop implementation.
package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	coreagent "github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/extension"
)

var ErrUnavailable = errors.New("agent loop is not active")

// Extension owns exactly one loop installation. Runtime is the admitted loop
// capability injected into scanners and session extensions.
type Extension struct{ runtime *Runtime }

// Runtime implements agent.Loop without exposing lifecycle operations.
type Runtime struct {
	loop coreagent.Loop

	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// New is inert. The supplied loop is the algorithm; Extension adds only
// lifecycle admission, cancellation and drain.
func New(loop coreagent.Loop) (*Extension, error) {
	if isNilLoop(loop) {
		return nil, fmt.Errorf("agent extension requires a loop")
	}
	return &Extension{runtime: &Runtime{loop: loop, done: make(chan struct{})}}, nil
}

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
	if err := scope.Init().Err(); err != nil {
		return err
	}
	r := e.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return ErrUnavailable
	}
	if r.lifetime == nil {
		r.lifetime, r.cancel = context.WithCancel(scope.Lifetime())
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context, config coreagent.Config) (*coreagent.Result, error) {
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
	// Recursive/derived runs retain the same admission boundary.
	config.Loop = r
	return r.loop.Run(call, config)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	return e.runtime.close(ctx)
}

func (r *Runtime) close(ctx context.Context) error {
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
	done := r.done
	r.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isNilLoop(loop coreagent.Loop) bool {
	if loop == nil {
		return true
	}
	value := reflect.ValueOf(loop)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

var _ extension.Extension = (*Extension)(nil)
var _ coreagent.Loop = (*Runtime)(nil)
