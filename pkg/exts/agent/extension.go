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

type Loop struct {
	loop     agent.Loop
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// Extension owns one loop installation.
type Extension struct{ loop *Loop }

func New(loop agent.Loop) *Extension {
	return &Extension{loop: &Loop{loop: loop, done: make(chan struct{})}}
}

// Loop lends business operations, never ownership of this installation.
func (e *Extension) Loop() *Loop {
	if e == nil {
		return nil
	}
	return e.loop
}
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.loop == nil || scope == nil {
		return ErrUnavailable
	}
	return e.loop.load(scope)
}

func (l *Loop) load(scope *extension.Scope) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return ErrUnavailable
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if l.loop == nil {
		return errors.New("agent extension requires a loop")
	}
	value := reflect.ValueOf(l.loop)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return errors.New("agent extension requires a non-nil loop")
		}
	}
	if l.lifetime == nil {
		l.lifetime, l.cancel = context.WithCancel(scope.Lifetime())
	}
	return nil
}

func (l *Loop) Run(ctx context.Context, config agent.Config) (*agent.Result, error) {
	if l == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.lifetime == nil || l.stopping || l.lifetime.Err() != nil {
		l.mu.Unlock()
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	l.active++
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.lifetime, cancel)
	l.mu.Unlock()
	defer func() {
		stop()
		cancel()
		l.mu.Lock()
		l.active--
		if l.stopping && l.active == 0 {
			close(l.done)
		}
		l.mu.Unlock()
	}()
	// Derived agent configs must retain the same lifecycle admission boundary.
	config.Loop = l
	return l.loop.Run(call, config)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.loop == nil {
		return nil
	}
	return e.loop.close(ctx)
}

func (l *Loop) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return
	}
	l.stopping = true
	if l.cancel != nil {
		l.cancel()
	}
	if l.active == 0 {
		close(l.done)
	}
}
func (l *Loop) close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.stop()
	select {
	case <-l.done:
		return nil
	default:
	}
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ extension.Extension = (*Extension)(nil)
var _ agent.Loop = (*Loop)(nil)
