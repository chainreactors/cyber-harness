package ioacmd

import (
	"github.com/chainreactors/aiscan/pkg/command"
	acpclient "github.com/chainreactors/ioa/client"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "ioa",
		Build: func(deps *command.Deps) []command.PseudoCommand {
			client, _ := deps.ACPClient.(acpclient.API)
			if client == nil {
				return nil
			}
			return NewCommands(client, deps.NodeName, deps.NodeMeta)
		},
	})
}
