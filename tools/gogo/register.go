package gogo

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "gogo", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "gogo", Summary: "gogo", UsageLine: "  gogo           Run gogo directly",
		Usage: func() string { return New(nil).Usage() }, Requires: []string{"scan.engine.Set.Gogo"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "gogo",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			es, ok := deps.Get(d.Bag, engine.SetKey)
			if !ok || es == nil || es.Gogo == nil {
				d.Skip("gogo", deps.Name(engine.SetKey)+".Gogo")
				return
			}
			impl := New(es.Gogo).WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithDataBus(d.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(), Run: impl.Run}, "scanner")
		},
	})
}
