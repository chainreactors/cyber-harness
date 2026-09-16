// Package files installs the basic file tools and owns their filesystem.
package files

import (
	"context"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/tools/files"
)

// Extension is the only files plugin and the sole lifecycle owner of Files.
type Extension struct {
	resource *files.Resource
}

func New(hookRegistry *hooks.Registry, config files.Config) (*Extension, error) {
	value, err := files.New(config, hookRegistry)
	if err != nil {
		return nil, err
	}
	return &Extension{resource: value}, nil
}

// Files returns the filesystem behavior. Its concrete type has no lifecycle
// methods; only this extension retains Resource.
func (e *Extension) Files() *files.Files {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Files
}

func (e *Extension) Load(scope *extension.Scope) error {
	if err := e.resource.Open(scope.Init()); err != nil {
		return err
	}
	tools, err := e.resource.Files.Tools()
	if err != nil {
		return err
	}
	return extension.Add(scope, tools...)
}

func (e *Extension) Close(ctx context.Context) error {
	return e.resource.Close(ctx)
}
