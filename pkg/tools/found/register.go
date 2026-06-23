package found

import (
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "found",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			cmd := New()
			cmd.SetWorkDir(deps.WorkDir)
			reg.Register(cmd, "found")
		},
	})
}
