// proxy command registers unconditionally

package proxy

import (
	"github.com/chainreactors/aiscan/pkg/commands"

	// Register extra proxy protocols so proxyclient.NewClient can handle them.
	_ "github.com/chainreactors/proxyclient/extra/anytls"
	_ "github.com/chainreactors/proxyclient/extra/clash"
	_ "github.com/chainreactors/proxyclient/extra/hysteria2"
	_ "github.com/chainreactors/proxyclient/extra/trojan"
	_ "github.com/chainreactors/proxyclient/extra/vmess"
)

// NewCommands constructs the native proxy command group without publishing it.
// Profiles that apply an authorization policy can wrap the returned command
// before the group becomes visible.
func NewCommands(execute CommandExecutor, hub *ProxyHub, fallbackProxy string) []commands.Command {
	state, store := (*State)(nil), (*FlowStore)(nil)
	if hub != nil {
		state, store = hub.state, hub.store
	} else {
		state, store = NewState(fallbackProxy), NewFlowStore(10000)
	}
	cmd := New(state)
	cmd.SetHub(hub)
	cmd.SetCommandExecutor(execute)
	proxyCommand := commands.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/proxy.md",
		Run:             cmd.Run,
	}

	mitmCmd := NewMitmCommand(store, hub)
	mitmCmd.SetCommandExecutor(execute)
	mitmCommand := commands.Command{
		Name: mitmCmd.Name(), Usage: mitmCmd.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/mitm.md",
		Run:             mitmCmd.Run,
	}
	return []commands.Command{proxyCommand, mitmCommand}
}
