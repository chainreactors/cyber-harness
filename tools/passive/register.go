//go:build full

// Passive uses the full-only uncover engine API; the standard stub does not
// expose QueryRaw, RawFofa or RawHunter, so this is a real implementation gate.

package passive

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "passive", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "passive", Summary: "passive", UsageLine: "  passive        Run passive cyberspace recon",
		Usage: func() string { return New(nil).Usage() }, Skills: []string{"passive"},
		Requires: []string{"scan.engine.Set.Uncover"},
	})

	commands.RegisterFactory(commands.Factory{
		Capability: "passive",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			// passive registers with a nil backend so its usage stays visible;
			// every query then reports that no recon source is configured.
			var backend QueryEngine
			if es, ok := deps.Get(d.Bag, engine.SetKey); ok && es != nil && es.Uncover != nil {
				backend = es.Uncover
			} else {
				d.Skip("passive.recon", deps.Name(engine.SetKey)+".Uncover")
			}
			impl := New(backend).WithLogger(d.GetLogger())
			reg.Register(commands.Command{
				Name: impl.Name(), Usage: impl.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/easm/passive.md",
				Run:             impl.Run,
			}, "scanner")
		},
	})
}
