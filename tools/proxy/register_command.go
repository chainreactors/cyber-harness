// proxy command registers unconditionally

package proxy

import (
	"context"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"

	// Register extra proxy protocols so proxyclient.NewClient can handle them.
	_ "github.com/chainreactors/proxyclient/extra/anytls"
	_ "github.com/chainreactors/proxyclient/extra/clash"
	_ "github.com/chainreactors/proxyclient/extra/hysteria2"
	_ "github.com/chainreactors/proxyclient/extra/trojan"
	_ "github.com/chainreactors/proxyclient/extra/vmess"
)

func init() {
	capability.Register(capability.Descriptor{ID: "proxy", Kind: capability.KindService, Group: "proxy"})
	commands.RegisterFactory(commands.Factory{
		Capability: "proxy",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			// The infrastructure (State + FlowStore + long-lived MITM hub) is
			// created by InstallInfra before BuildPlan so Deps.ScannerProxy
			// already points every tool at the stable hub address. Here we only
			// register the verbs that observe and steer it.
			state, store, hub := resolveInfra(d)

			cmd := New(state)
			cmd.SetHub(hub)
			cmd.SetCommandExecutor(reg.Run)
			reg.Register(commands.Command{
				Name: cmd.Name(), Usage: cmd.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/runtime/proxy.md",
				Run:             cmd.Run,
			}, "proxy")

			mitmCmd := NewMitmCommand(reg, store, hub)
			mitmCmd.SetCommandExecutor(reg.Run)
			reg.Register(commands.Command{
				Name: mitmCmd.Name(), Usage: mitmCmd.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/runtime/mitm.md",
				Run:             mitmCmd.Run,
				Close: func() {
					if hub != nil {
						hub.Shutdown(context.Background())
					}
				},
			}, "proxy")
		},
	})
}

// resolveInfra returns the shared proxy infrastructure installed by
// InstallInfra, or a hub-less fallback (direct egress, no capture) for build
// paths — chiefly tests — that register the proxy group without it.
func resolveInfra(d *commands.Deps) (*State, *FlowStore, *ProxyHub) {
	if d.Bag != nil {
		if infra, ok := deps.Get(d.Bag, InfraKey); ok && infra != nil {
			return infra.State, infra.Store, infra.Hub
		}
	}
	return NewState(d.ScannerProxy), NewFlowStore(10000), nil
}
