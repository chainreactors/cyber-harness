package gogo

import (
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
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
