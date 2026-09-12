package ioa

import (
	"fmt"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"

	_ "github.com/chainreactors/ioa/protocols/checkpoint"
	_ "github.com/chainreactors/ioa/protocols/handoff"
	_ "github.com/chainreactors/ioa/protocols/swarm"
)

func Register(reg *commands.Registry, client protocols.ClientAPI, nodeName string, nodeMeta map[string]any) error {
	if client == nil {
		return fmt.Errorf("IOA client is required")
	}
	return reg.Register("ioa", "ioa", NewCommands(client, nodeName, nodeMeta)...)
}
