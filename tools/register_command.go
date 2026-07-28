package tools

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "scan", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "scan", Summary: "scan", Usage: scan.Usage,
		Requires: []string{"scan.engine.Set.Gogo", "scan.engine.Set.Spray"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "scan",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			es, ok := deps.Get(d.Bag, engine.SetKey)
			if !ok || es == nil || es.Gogo == nil || es.Spray == nil {
				d.Skip("scan", deps.Name(engine.SetKey)+".Gogo+.Spray")
				return
			}

			// copy: the bag's slice is shared with every other build
			stored, _ := deps.Get(d.Bag, scan.OptsKey)
			scanOpts := append([]scan.Option(nil), stored...)
			if d.ScannerProxy != "" {
				scanOpts = append(scanOpts, scan.WithProxy(d.ScannerProxy))
			}
			if d.DataBus != nil {
				scanOpts = append(scanOpts, scan.WithDataBus(d.DataBus))
			}

			impl := scan.New(es, scanOpts...)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "scanner")
		},
	})
}
