// Package console contributes Session commands to an explicitly installed TUI.
// Session execution and the headless protocol remain in agent/session.
package console

import (
	"fmt"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	cmdline "github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/console/api"
	"github.com/spf13/cobra"
)

type Extension struct {
	runtime *agentsession.Runtime
}

func New() *Extension { return &Extension{} }

func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	runtime, err := extension.Use[*agentsession.Runtime](scope)
	if err != nil {
		return err
	}
	e.runtime = runtime
	return extension.Add(scope, bindings(runtime))
}

// bindings snapshots the runtime commands at installation. Runtime additions remain
// available through the Session protocol; a TUI profile is a fixed installation.
func bindings(runtime *agentsession.Runtime) *api.Bindings {
	specs := runtime.CommandSpecs(false)
	return &api.Bindings{Commands: func(view api.View) []*cobra.Command {
		var commands []*cobra.Command
		for _, spec := range specs {
			if spec.Name == "/help" {
				continue
			} // TUI owns its complete command panel.
			usage := spec.Usage
			if usage == "" {
				usage = spec.Name
			}
			name := spec.Name
			commands = append(commands, &cobra.Command{Use: usage, Aliases: append([]string(nil), spec.Aliases...), Short: spec.Description, DisableFlagParsing: true,
				RunE: func(_ *cobra.Command, args []string) error {
					if view.Command == nil {
						return fmt.Errorf("session command requires an attached terminal")
					}
					if err := view.Command(cmdline.JoinCommandLine(name, args)); err != nil {
						return err
					}
					if name == "/status" && view.RefreshStatus != nil {
						view.RefreshStatus()
					}
					return nil
				},
			})
		}
		return commands
	}}
}

var _ extension.Extension = (*Extension)(nil)
