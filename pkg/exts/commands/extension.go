// Package commands contributes immutable native command groups to a Registry.
package commands

import (
	"context"
	"errors"
	"slices"

	"github.com/chainreactors/aiscan/core/extension"
	commandpkg "github.com/chainreactors/aiscan/pkg/commands"
)

type Extension struct {
	registry *commandpkg.Registry
	group    string
	values   []commandpkg.Command
}

func New(registry *commandpkg.Registry, group string, values ...commandpkg.Command) (*Extension, error) {
	if registry == nil || len(values) == 0 {
		return nil, errors.New("command extension requires a registry and commands")
	}
	return &Extension{registry: registry, group: group, values: slices.Clone(values)}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	return e.registry.Register(scope, e.group, e.values...)
}

func (e *Extension) Close(context.Context) error {
	e.values = nil
	return nil
}

var _ extension.Extension = (*Extension)(nil)
