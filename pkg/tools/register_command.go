package tools

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func init() {
	cfg.ExtraScannerUsage["scan"] = scan.Usage
	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil {
				return
			}

			var scanOpts []scan.Option
			for _, o := range deps.ScanOpts {
				if opt, ok := o.(scan.Option); ok {
					scanOpts = append(scanOpts, opt)
				}
			}
			if deps.ScannerProxy != "" {
				scanOpts = append(scanOpts, scan.WithProxy(deps.ScannerProxy))
			}
			if deps.DataBus != nil {
				scanOpts = append(scanOpts, scan.WithDataBus(deps.DataBus))
			}

			if es.Gogo != nil && es.Spray != nil {
				impl := scan.New(es, scanOpts...)
				reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run}, "scanner")
			}
		},
	})
}
