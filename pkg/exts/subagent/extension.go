// Package subagent installs the Subagent Point and its optional session tool.
package subagent

import (
	"context"

	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/agent/subagent/sessionexec"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type Extension struct{ registry *subagent.Registry }

func New() *Extension { return &Extension{registry: subagent.NewRegistry()} }

func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := extension.Define[subagent.Subagent](scope, e.registry); err != nil {
		return err
	}
	if err := extension.Provide[*subagent.Registry](scope, e.registry); err != nil {
		return err
	}
	if err := extension.Provide[subagent.Executor](scope, e.registry); err != nil {
		return err
	}
	return e.registry.Activate(scope.Lifetime())
}

func (e *Extension) Close(ctx context.Context) error { return e.registry.Close(ctx) }

// ToolsExtension borrows the existing Point. It owns only session dispatch and
// the model-facing tool, so session-free consumers need not install a Runtime.
type ToolsExtension struct{ tool *sessionexec.Tool }

func NewTools() *ToolsExtension { return &ToolsExtension{} }

func (e *ToolsExtension) Load(scope *extension.Scope) error {
	executor, err := extension.Use[subagent.Executor](scope)
	if err != nil {
		return err
	}
	runtime, err := extension.Use[*session.Runtime](scope)
	if err != nil {
		return err
	}
	e.tool = sessionexec.New(runtime, executor, scope.Lifetime())
	return extension.Add[coretool.Tool](scope, e.tool)
}

func (e *ToolsExtension) Close(ctx context.Context) error {
	if e.tool == nil {
		return nil
	}
	return e.tool.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
var _ extension.Extension = (*ToolsExtension)(nil)
