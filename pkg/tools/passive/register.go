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
			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil || es.Uncover == nil {
				return
			}
			logger := deps.GetLogger()
			reg.Register(New(es.Uncover).WithLogger(logger), "scanner")
		},
	})
}
