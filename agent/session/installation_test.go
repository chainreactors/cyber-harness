package session

import (
	"context"
	"errors"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
)

// Legacy integration scenarios exercise both resources through a test fixture.
// Production profiles install separate Agent and Session nodes.
type Extension struct {
	runtime  *Runtime
	loop     *loopext.Extension
	sessions bool
}

func New(c Config) (*Extension, error) {
	sessions := c.Application != nil
	var loop *loopext.Extension
	if c.Loop != nil || !sessions {
		loop = loopext.New(c.Loop)
		c.Loop = loop.Runtime()
	}
	r, err := NewManager(c)
	if err != nil {
		return nil, err
	}
	return &Extension{runtime: r, loop: loop, sessions: sessions}, nil
}
func (e *Extension) Runtime() *Runtime { return e.runtime }
func (e *Extension) Load(s *extension.Scope) error {
	if e.loop != nil {
		if err := e.loop.Load(s); err != nil {
			return err
		}
	}
	if e.sessions {
		return e.runtime.Start(s.Init(), s.Lifetime())
	}
	return nil
}
func (e *Extension) Close(ctx context.Context) error {
	var err error
	if e.loop != nil {
		err = e.loop.Close(ctx)
	}
	if e.sessions {
		err = errors.Join(err, e.runtime.Close(ctx))
	}
	return err
}
func (r *Runtime) Run(ctx context.Context, c agent.Config) (*agent.Result, error) {
	if r.runtimeConfig.Loop == nil {
		return nil, ErrUnavailable
	}
	result, err := r.runtimeConfig.Loop.Run(ctx, c)
	if errors.Is(err, loopext.ErrUnavailable) {
		err = errors.Join(err, ErrUnavailable)
	}
	return result, err
}

func testEnvironment(value any) *apppkg.App {
	application, _ := value.(*apppkg.App)
	return application
}
