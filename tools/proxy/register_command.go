// proxy command registers unconditionally

package proxy

import (
	coretool "github.com/chainreactors/cyber/core/tool"

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
func NewCommands(execute CommandExecutor, hub *ProxyHub, fallbackProxy string) []coretool.Command {
	state, store := (*State)(nil), (*FlowStore)(nil)
	if hub != nil {
		state, store = hub.state, hub.store
	} else {
		state, store = NewState(fallbackProxy), NewFlowStore(10000)
	}
	cmd := New(state)
	cmd.SetHub(hub)
	cmd.SetCommandExecutor(execute)
	proxyCommand := coretool.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "cyber://skills/runtime/proxy.md",
		Run:             cmd.Run,
	}

	mitmCmd := NewMitmCommand(store, hub)
	mitmCmd.SetCommandExecutor(execute)
	mitmCommand := coretool.Command{
		Name: mitmCmd.Name(), Usage: mitmCmd.Usage(),
		DescriptionPath: "cyber://skills/runtime/mitm.md",
		Run:             mitmCmd.Run,
	}
	return []coretool.Command{proxyCommand, mitmCommand}
}
