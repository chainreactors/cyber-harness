// Package tmux contributes the tmux command to the command point, built on the
// Bash tool the terminal extension owns.
//
// It is separate from the terminal extension because tmux is a session policy,
// not part of owning a shell: a host that wants different session ownership
// installs a different extension here instead of handing the terminal a
// constructor callback it would have to call back into.
package tmux

import (
	"fmt"

	"github.com/chainreactors/cyber/core/extension"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type Extension struct{}

func New() *Extension { return &Extension{} }

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("tmux extension is unavailable")
	}
	bash, err := extension.Use[*terminaltool.BashTool](scope)
	if err != nil {
		return err
	}
	return extension.Add(scope, terminaltool.NewTmuxCommand(bash))
}

var _ extension.Extension = (*Extension)(nil)
