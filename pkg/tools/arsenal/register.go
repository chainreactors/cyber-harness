package arsenal

import "github.com/chainreactors/aiscan/pkg/commands"

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "arsenal",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger := deps.GetLogger()

			cmd, err := NewArsenalCommand()
			if err != nil {
				logger.Warnf("arsenal init: %v", err)
				return
			}
			reg.Register(commands.Command{Name: cmd.Name(), Usage: cmd.Usage(), Run: cmd.Run}, "arsenal")
		},
	})
}
