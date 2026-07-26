package ioa

import (
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"

	_ "github.com/chainreactors/ioa/protocols/checkpoint"
	_ "github.com/chainreactors/ioa/protocols/handoff"
	_ "github.com/chainreactors/ioa/protocols/swarm"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "ioa",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			client, _ := deps.IOAClient.(protocols.ClientAPI)
			if client == nil {
				return
			}
			for _, cmd := range NewCommands(client, deps.NodeName, deps.NodeMeta) {
				reg.Register(cmd, "ioa")
			}
		},
	})
}
