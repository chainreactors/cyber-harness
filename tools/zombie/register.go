package zombie

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "zombie", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "zombie", Summary: "zombie", UsageLine: "  zombie         Run zombie directly",
		Usage: func() string { return New(nil).Usage() }, Requires: []string{"scan.engine.Set.Zombie"},
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "zombie",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			es, ok := deps.Get(d.Bag, engine.SetKey)
			if !ok || es == nil || es.Zombie == nil {
				d.Skip("zombie", deps.Name(engine.SetKey)+".Zombie")
				return
			}
			impl := New(es.Zombie).WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithDataBus(d.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "scanner")
		},
	})
}
