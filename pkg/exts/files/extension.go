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
	config   files.Config
	resource *files.Resource
}

func New(config files.Config) *Extension {
	return &Extension{config: config}
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
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	if e.resource, err = files.New(e.config, registry); err != nil {
		return err
	}
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
