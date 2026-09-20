// Package native contributes the built-in Agent tools and commands.
package native

import (
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	looptool "github.com/chainreactors/cyber/tools/loop"
)

type Extension struct{}

func New() *Extension { return &Extension{} }
func (e *Extension) Load(scope *extension.Scope) error {
	if err := extension.Add[coretool.Tool](scope, &agent.InboxWaitTool{}); err != nil {
		return err
	}
	return extension.Add(scope, looptool.NewCommand())
}

var _ extension.Extension = (*Extension)(nil)
