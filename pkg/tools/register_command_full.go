//go:build full

package tools

import (
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	gogocmd "github.com/chainreactors/aiscan/pkg/tools/gogo"
	katanacmd "github.com/chainreactors/aiscan/pkg/tools/katana"
	neutroncmd "github.com/chainreactors/aiscan/pkg/tools/neutron"
	passivecmd "github.com/chainreactors/aiscan/pkg/tools/passive"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	spraycmd "github.com/chainreactors/aiscan/pkg/tools/spray"
	zombiecmd "github.com/chainreactors/aiscan/pkg/tools/zombie"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "scanner",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			reg.Register(katanacmd.New(), "scanner")

			es, _ := deps.EngineSet.(*engine.Set)
			if es == nil {
				return
			}
			logger, _ := deps.Logger.(telemetry.Logger)
			if logger == nil {
				logger = telemetry.NopLogger()
			}
			proxy := deps.ScannerProxy

			if es.Uncover != nil {
				reg.Register(passivecmd.New(es.Uncover).WithLogger(logger), "scanner")
			}

			var scanOpts []scan.Option
			for _, o := range deps.ScanOpts {
				if opt, ok := o.(scan.Option); ok {
					scanOpts = append(scanOpts, opt)
				}
			}
			if proxy != "" {
				scanOpts = append(scanOpts, scan.WithProxy(proxy))
			}

			if es.Gogo != nil && es.Spray != nil {
				reg.Register(scan.New(es, scanOpts...), "scanner")
			}
			if es.Gogo != nil {
				reg.Register(gogocmd.New(es.Gogo).WithLogger(logger).WithProxy(proxy), "scanner")
			}
			if es.Spray != nil {
				reg.Register(spraycmd.New(es.Spray).WithLogger(logger).WithProxy(proxy), "scanner")
			}
			if es.Zombie != nil {
				reg.Register(zombiecmd.New(es.Zombie).WithLogger(logger).WithProxy(proxy), "scanner")
			}
			if es.Neutron != nil {
				reg.Register(neutroncmd.New(es.Neutron, es.Index).WithLogger(logger).WithProxy(proxy), "scanner")
			}
		},
	})
}
