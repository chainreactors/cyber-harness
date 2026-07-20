package spray

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
	cfg.ExtraScannerUsage["spray"] = func() string { return New(nil).Usage() }
	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil || es.Spray == nil {
				return
			}
			reg.Register(
				New(es.Spray).WithLogger(deps.GetLogger()).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus),
				"scanner",
			)
		},
	})
}
