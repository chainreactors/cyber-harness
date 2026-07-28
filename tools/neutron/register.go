package neutron

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "neutron", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "neutron", Summary: "neutron", UsageLine: "  neutron        Run neutron directly",
		Usage: func() string { return New(nil, nil).Usage() }, Requires: []string{"scan.engine.Set.Neutron"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "neutron",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			es, ok := deps.Get(d.Bag, engine.SetKey)
			if !ok || es == nil || es.Neutron == nil {
				d.Skip("neutron", deps.Name(engine.SetKey)+".Neutron")
				return
			}
			impl := New(es.Neutron, es.Index).WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithDataBus(d.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(), Run: impl.Run}, "scanner")
		},
	})
}
