package ioa

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"

	_ "github.com/chainreactors/ioa/protocols/checkpoint"
	_ "github.com/chainreactors/ioa/protocols/handoff"
	_ "github.com/chainreactors/ioa/protocols/swarm"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "ioa", Kind: capability.KindService, Group: "ioa",
		Requires: []string{"ioa.ClientAPI"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "ioa",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			client, ok := deps.Get(d.Bag, ClientKey)
			if !ok || client == nil {
				d.Skip("ioa", deps.Name(ClientKey))
				return
			}
			for _, cmd := range NewCommands(client, d.NodeName, d.NodeMeta) {
				reg.Register(cmd, "ioa")
			}
		},
	})
}
