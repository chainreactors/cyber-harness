package tools

import (
	"fmt"

	cyberhubcmd "github.com/chainreactors/aiscan/pkg/tools/cyberhub"
	"github.com/chainreactors/aiscan/pkg/tools/engines"
	gogocmd "github.com/chainreactors/aiscan/pkg/tools/gogo"
	neutroncmd "github.com/chainreactors/aiscan/pkg/tools/neutron"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	spraycmd "github.com/chainreactors/aiscan/pkg/tools/spray"
	zombiecmd "github.com/chainreactors/aiscan/pkg/tools/zombie"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func RegisterAll(reg *ScannerRegistry, engineSet *engines.Set, opts ...scan.Option) error {
	return RegisterAllWithLogger(reg, engineSet, telemetry.NopLogger(), opts...)
}

func RegisterAllWithLogger(reg *ScannerRegistry, engineSet *engines.Set, logger telemetry.Logger, opts ...scan.Option) error {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if engineSet == nil {
		engineSet = &engines.Set{}
	}
	if engineSet.Resources != nil {
		reg.Register(cyberhubcmd.New(engineSet.Resources))
	}
	if engineSet.Gogo != nil && engineSet.Spray != nil {
		reg.Register(scan.New(engineSet, opts...))
	}
	if engineSet.Gogo != nil {
		reg.Register(gogocmd.New(engineSet.Gogo))
	}
	if engineSet.Spray != nil {
		reg.Register(spraycmd.New(engineSet.Spray))
	}
	if engineSet.Zombie != nil {
		reg.Register(zombiecmd.New(engineSet.Zombie))
	}
	if engineSet.Neutron != nil {
		reg.Register(neutroncmd.New(engineSet.Neutron, engineSet.Index).WithLogger(logger))
	}

	logger.Infof("scanner commands=%s", fmt.Sprintf("%v", reg.Names()))
	return nil
}
