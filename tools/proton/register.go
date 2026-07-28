package proton

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "proton", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "proton", Summary: "proton", UsageLine: "  proton         Run proton sensitive info scanner",
		Usage: func() string { return New().Usage() },
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "proton",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			cmd := New().WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithDataBus(d.DataBus)
			if rs, ok := deps.Get(d.Bag, resources.SetKey); ok && rs != nil {
				cmd.WithResourceProvider(rs.ProtonConfig)
			} else {
				// proton still runs, but only with its built-in rules.
				d.Skip("proton.rules", deps.Name(resources.SetKey))
			}
			cmd.SetWorkDir(d.WorkDir)
			reg.Register(commands.Command{Name: cmd.Name(), Usage: cmd.Usage(), Run: cmd.Run}, "scanner")
		},
	})
}
