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
		Build: buildScannerCommands,
	})
}

func buildScannerCommands(deps *command.Deps) []command.PseudoCommand {
	es, _ := deps.EngineSet.(*engines.Set)
	if es == nil {
		return nil
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

	var cmds []command.PseudoCommand
	if es.Gogo != nil && es.Spray != nil {
		cmds = append(cmds, scan.New(es, scanOpts...))
	}
	if es.Gogo != nil {
		cmds = append(cmds, gogocmd.New(es.Gogo))
	}
	if es.Spray != nil {
		cmds = append(cmds, spraycmd.New(es.Spray))
	}
	if es.Zombie != nil {
		cmds = append(cmds, zombiecmd.New(es.Zombie))
	}
	if es.Neutron != nil {
		cmds = append(cmds, neutroncmd.New(es.Neutron, es.Index).WithLogger(logger))
	}
	return cmds
}
