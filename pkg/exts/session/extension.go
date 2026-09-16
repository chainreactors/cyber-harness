// Package session installs the harness-independent agent/session manager.
package session

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
)

type Extension struct {
	runtime *session.Runtime
}

func New(config session.Config) (*Extension, error) {
	runtime, err := session.NewManager(config)
	if err != nil {
		return nil, err
	}
	return &Extension{runtime: runtime}, nil
}
func (e *Extension) Runtime() *session.Runtime {
	if e == nil {
		return nil
	}
	return e.runtime
}
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.runtime == nil || scope == nil {
		return fmt.Errorf("session extension is unavailable")
	}
	return e.runtime.Start(scope.Init(), scope.Lifetime())
}
func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	return e.runtime.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
