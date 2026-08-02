package arsenal

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	capability.Register(capability.Descriptor{ID: "arsenal", Kind: capability.KindTool, Group: "arsenal"})
	commands.RegisterFactory(commands.Factory{
		Capability: "arsenal",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger := deps.GetLogger()

			cmd, err := NewArsenalCommand()
			if err != nil {
				logger.Warnf("arsenal init: %v", err)
				return
			}
			reg.Register(commands.Command{
				Name: cmd.Name(), Usage: cmd.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/runtime/arsenal.md",
				Run:             cmd.Run,
			}, "arsenal")
		},
	})
}
