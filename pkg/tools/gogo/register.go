package gogo

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
	cfg.ExtraScannerUsage["gogo"] = func() string { return New(nil).Usage() }
	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil || es.Gogo == nil {
				return
			}
			reg.Register(
				New(es.Gogo).WithLogger(deps.GetLogger()).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus),
				"scanner",
			)
		},
	})
}
