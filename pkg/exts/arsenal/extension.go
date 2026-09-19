// Package arsenal owns package-manager initialization and its command declaration.
package arsenal

import (
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	tool "github.com/chainreactors/cyber/tools/arsenal"
	"path/filepath"
)

type Extension struct {
	directory string
}

func New(directory string) (*Extension, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("arsenal requires an absolute directory")
	}
	return &Extension{directory: directory}, nil
}
func (e *Extension) BinDir() string { return filepath.Join(e.directory, "bin") }
func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	command, err := tool.NewCommand(e.directory)
	if err != nil {
		return err
	}
	return extension.Add(scope, command)
}
