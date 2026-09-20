package client

import (
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/console/api"
	"github.com/chainreactors/cyber/tools/ioa"
)

// ConsoleExtension installs IOA presentation only when a profile selects a TUI.
type ConsoleExtension struct {
	bindings *api.Bindings
}

func NewConsole(reader ioa.Reader, space, endpoint string) (*ConsoleExtension, error) {
	if reader == nil {
		return nil, fmt.Errorf("IOA presentation requires IOA reader")
	}
	return &ConsoleExtension{bindings: ConsoleBindings(reader, space, endpoint)}, nil
}
func (e *ConsoleExtension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	return extension.Add(scope, e.bindings)
}

var _ extension.Extension = (*ConsoleExtension)(nil)
