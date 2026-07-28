package spray

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "spray", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "spray", Summary: "spray", UsageLine: "  spray          Run spray directly",
		Usage: func() string { return New(nil).Usage() }, Requires: []string{"scan.engine.Set.Spray"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "spray",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			es, ok := deps.Get(d.Bag, engine.SetKey)
			if !ok || es == nil || es.Spray == nil {
				d.Skip("spray", deps.Name(engine.SetKey)+".Spray")
				return
			}
			impl := New(es.Spray).WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithDataBus(d.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(), Run: impl.Run}, "scanner")
		},
	})
}
