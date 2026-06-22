//go:build full

package playwright

import "github.com/chainreactors/aiscan/pkg/commands"

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "browser",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			reg.Register(New(deps.WorkDir), "browser")
		},
	})
}
