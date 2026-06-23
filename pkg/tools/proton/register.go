package proton

import (
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "proton",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger, _ := deps.Logger.(telemetry.Logger)
			if logger == nil {
				logger = telemetry.NopLogger()
			}
			cmd := New().WithLogger(logger)
			cmd.SetWorkDir(deps.WorkDir)
			reg.Register(cmd, "proton")
		},
	})
}
