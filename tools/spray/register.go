package spray

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
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
			impl := New(es.Spray).WithLogger(deps.GetLogger()).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(), Run: impl.Run, SetProxy: impl.SetProxy, GetProxy: func() string { return impl.Proxy }}, "scanner")
		},
	})
}
