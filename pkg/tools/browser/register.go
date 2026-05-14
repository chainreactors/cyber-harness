//go:build browser

package browser

import "github.com/chainreactors/aiscan/pkg/command"

func init() {
	command.RegisterFactory(command.Factory{
		Group: "tools",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			reg.Register(New(deps.WorkDir), "tools")
		},
	})
}
