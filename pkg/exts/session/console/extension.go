// Package console contributes Session commands to an explicitly installed TUI.
// Session execution and the headless protocol remain in agent/session.
package console

import (
	"context"
	"fmt"
	"github.com/chainreactors/aiscan/core/commandline"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/console/api"
	"github.com/chainreactors/aiscan/pkg/types"
	"github.com/spf13/cobra"
)

type Catalog interface {
	CommandSpecs(bool) []*types.CommandSpec
}
type Extension struct {
	target  api.Registrar
	catalog Catalog
}

func New(target api.Registrar, catalog Catalog) (*Extension, error) {
	if target == nil || catalog == nil {
		return nil, fmt.Errorf("session presentation requires TUI registrar and session catalog")
	}
	return &Extension{target: target, catalog: catalog}, nil
}
func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	return e.target.Register(api.Contribution{Source: "session", Bindings: Bind(e.catalog)})
}
func (*Extension) Close(context.Context) error { return nil }

// Bind snapshots the command catalog at installation. Runtime additions remain
// available through the Session protocol; a TUI profile is a fixed installation.
func Bind(catalog Catalog) *api.Bindings {
	specs := catalog.CommandSpecs(false)
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
					if err := view.Command(commandline.JoinCommandLine(name, args)); err != nil {
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
