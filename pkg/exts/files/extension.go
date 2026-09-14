// Package files installs the basic file tools and owns their filesystem.
package files

import (
	"context"
	"errors"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/files"
)

// Extension is the only files plugin and the sole lifecycle owner of Files.
type Extension struct {
	resource *files.Resource
	registry toolset.Registrar
}

func New(registry toolset.Registrar, hookRegistry *hooks.Registry, config files.Config) (*Extension, error) {
	if registry == nil {
		return nil, errors.New("files extension requires a tool registry")
	}
	value, err := files.New(config, hookRegistry)
	if err != nil {
		return nil, err
	}
	return &Extension{resource: value, registry: registry}, nil
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
	return e.registry.Register("files", tools...)
}

func (e *Extension) Close(ctx context.Context) error {
	return e.resource.Close(ctx)
}
