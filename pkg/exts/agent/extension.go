// Package agent gives an agent loop an explicitly owned run lifetime.
package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/extension"
)

var ErrUnavailable = errors.New("agent extension is not active")

// Extension owns one loop instance. Provider, executor and other dependencies
// are borrowed through Config. Run is its only execution entry point.
type Extension struct {
	config   agent.Config
	mu       sync.Mutex
	loop     *agent.Agent
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

// New is inert; callers supply the loop implementation and its dependencies.
func New(config agent.Config) *Extension {
	return &Extension{config: config, done: make(chan struct{})}
}

func (e *Extension) Load(scope *extension.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return ErrUnavailable
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if e.loop != nil {
		return nil
	}
	e.lifetime, e.cancel = context.WithCancel(scope.Lifetime())
	config := e.config
	if config.Tools == nil {
		config.Tools = scope.Executor()
	}
	e.loop = agent.NewAgent(config)
	return nil
}

func (e *Extension) Run(ctx context.Context, input *aop.Message, options ...agent.RunOption) (*agent.Result, error) {
	e.mu.Lock()
	if e.loop == nil || e.stopping || e.lifetime.Err() != nil {
		e.mu.Unlock()
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.active++
	loop := e.loop
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.lifetime, cancel)
	e.mu.Unlock()
	defer func() {
		stop()
		cancel()
		e.mu.Lock()
		e.active--
		if e.stopping && e.active == 0 {
			close(e.done)
		}
		e.mu.Unlock()
	}()
	return loop.Run(call, input, options...)
}

func (e *Extension) Close(ctx context.Context) error {
	e.mu.Lock()
	if !e.stopping {
		e.stopping = true
		if e.cancel != nil {
			e.cancel()
		}
		if e.active == 0 {
			close(e.done)
		}
	}
	e.mu.Unlock()
	select {
	case <-e.done:
		return nil
	default:
	}
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
}

var _ extension.Extension = (*Extension)(nil)
