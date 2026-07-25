//go:build full

package passive

import (
	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
	config.ExtraCommands["passive"] = true
	config.ExtraUsageEntries = append(config.ExtraUsageEntries, "  passive        Run passive cyberspace recon")
	config.ExtraSummaryEntries = append(config.ExtraSummaryEntries, "passive")
	config.ExtraScannerUsage["passive"] = func() string { return New(nil).Usage() }

	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			var backend QueryEngine
			if es, ok := deps.EngineSet.(*engine.Set); ok && es != nil && es.Uncover != nil {
				backend = es.Uncover
			}
			logger := deps.GetLogger()
			impl := New(backend).WithLogger(logger)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "scanner")
		},
	})
}
