// Package files installs the basic file tools and owns their filesystem.
package files

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/tools/files"
)

// Extension is the only files plugin. Files is the borrowed raw implementation.
type Extension struct{ *files.Files }

func New(config files.Config) (*Extension, error) {
	value, err := files.New(config)
	if err != nil {
		return nil, err
	}
	return &Extension{Files: value}, nil
}

func (e *Extension) Load(scope *extension.Context) error {
	if err := e.Files.Open(scope.Init()); err != nil {
		return err
	}
	tools, err := e.Files.Tools()
	if err != nil {
		return err
	}
	return scope.RegisterTools(tools...)
}

func (e *Extension) Close(ctx context.Context) error {
	err := e.Files.Close(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return err
}
