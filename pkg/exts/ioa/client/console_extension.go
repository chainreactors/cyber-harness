package client

import (
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/tools/ioa"
)

type ConsoleConfig struct{ Space, Endpoint string }
type ConsoleExtension struct{ options ConsoleConfig }

func NewConsole(options ConsoleConfig) *ConsoleExtension {
	return &ConsoleExtension{options: options}
}
func (e *ConsoleExtension) Load(scope *extension.Scope) error {
	reader, err := extension.Use[*ioa.Service](scope)
	if err != nil {
		return err
	}
	return extension.Add(scope, consoleBindings(reader, e.options.Space, e.options.Endpoint))
}

var _ extension.Extension = (*ConsoleExtension)(nil)
