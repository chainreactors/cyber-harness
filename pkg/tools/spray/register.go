package spray

import (
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil || es.Spray == nil {
				return
			}
			reg.Register(
				New(es.Spray).WithLogger(deps.GetLogger()).WithProxy(deps.ScannerProxy),
				"scanner",
			)
		},
	})
}
