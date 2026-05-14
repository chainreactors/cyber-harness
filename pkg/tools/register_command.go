package tools

import (
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/tools/engines"
	gogocmd "github.com/chainreactors/aiscan/pkg/tools/gogo"
	neutroncmd "github.com/chainreactors/aiscan/pkg/tools/neutron"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	spraycmd "github.com/chainreactors/aiscan/pkg/tools/spray"
	zombiecmd "github.com/chainreactors/aiscan/pkg/tools/zombie"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "scanner",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			es, _ := deps.EngineSet.(*engines.Set)
			if es == nil {
				return
			}
			logger, _ := deps.Logger.(telemetry.Logger)
			if logger == nil {
				logger = telemetry.NopLogger()
			}

			var scanOpts []scan.Option
			for _, o := range deps.ScanOpts {
				if opt, ok := o.(scan.Option); ok {
					scanOpts = append(scanOpts, opt)
				}
			}

			if es.Gogo != nil && es.Spray != nil {
				reg.Register(scan.New(es, scanOpts...), "scanner")
			}
			if es.Gogo != nil {
				reg.Register(gogocmd.New(es.Gogo), "scanner")
			}
			if es.Spray != nil {
				reg.Register(spraycmd.New(es.Spray), "scanner")
			}
			if es.Zombie != nil {
				reg.Register(zombiecmd.New(es.Zombie), "scanner")
			}
			if es.Neutron != nil {
				reg.Register(neutroncmd.New(es.Neutron, es.Index).WithLogger(logger), "scanner")
			}
		},
	})
}
