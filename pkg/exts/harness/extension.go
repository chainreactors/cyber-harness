// Package harness provides the profile-level execution infrastructure used by
// capability extensions. It owns registries; capability extensions only borrow
// their registration and execution surfaces.
package harness

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

type Extension struct {
	tools    *toolset.Registry
	commands *commands.Registry
}

func (e *Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "harness", Description: "tool and command infrastructure", Provides: []extension.Service{extension.ServiceOf[tool.Executor]("tools.executor"), extension.ServiceOf[commands.Executor]("commands.executor")}}
}

// New constructs collecting registries without opening external resources.
func New(hooks *hooks.Registry) (*Extension, error) {
	if hooks == nil {
		return nil, fmt.Errorf("harness requires hooks")
	}
	return &Extension{tools: toolset.NewRegistry(hooks), commands: commands.NewRegistry(hooks)}, nil
}

func (e *Extension) Tools() tool.Executor {
	if e == nil {
		return nil
	}
	return e.tools
}
func (e *Extension) ToolRegistry() toolset.Runtime {
	if e == nil {
		return nil
	}
	return e.tools
}
func (e *Extension) Commands() commands.Runtime {
	if e == nil {
		return nil
	}
	return e.commands
}

// Load activates registries after contributors have registered their values.
// Profiles should place this entry after contributor entries in the graph.
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.tools == nil || e.commands == nil || scope == nil {
		return fmt.Errorf("harness is unavailable")
	}
	if err := e.commands.Load(scope); err != nil {
		return err
	}
	return e.tools.Load(scope)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if e.tools != nil {
		if err := e.tools.Close(ctx); err != nil {
			return err
		}
	}
	if e.commands != nil {
		return e.commands.Close(ctx)
	}
	return nil
}

var _ extension.Extension = (*Extension)(nil)
