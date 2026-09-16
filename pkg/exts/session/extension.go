// Package session installs the harness-independent session runtime.
package session

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
)

type Extension struct {
	resource *session.Resource
}

func New(config session.Config) (*Extension, error) {
	resource, err := session.NewResource(config)
	if err != nil {
		return nil, err
	}
	return &Extension{resource: resource}, nil
}
func (e *Extension) Runtime() *session.Runtime {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Runtime()
}
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.resource == nil || scope == nil {
		return fmt.Errorf("session extension is unavailable")
	}
	return e.resource.Start(scope.Init(), scope.Lifetime())
}
func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
