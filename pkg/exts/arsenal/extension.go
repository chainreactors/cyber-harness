// Package arsenal owns package-manager initialization and its command declaration.
package arsenal

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
	tool "github.com/chainreactors/cyber/tools/arsenal"
	"path/filepath"
)

const ID = "arsenal"

type Extension struct {
	directory string
	commands  commands.Runtime
}

func New(directory string, registry commands.Runtime) (*Extension, error) {
	if !filepath.IsAbs(directory) || registry == nil {
		return nil, fmt.Errorf("arsenal requires an absolute directory and commands")
	}
	return &Extension{directory: directory, commands: registry}, nil
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
	return e.commands.Register(ID, ID, command)
}

// The manager owns persistent files, not open handles. Registry drains its calls.
func (e *Extension) Close(context.Context) error { return nil }
