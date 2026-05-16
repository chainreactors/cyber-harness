package tools

import (
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	cyberhubcmd "github.com/chainreactors/aiscan/pkg/tools/cyberhub"
	gogocmd "github.com/chainreactors/aiscan/pkg/tools/gogo"
	neutroncmd "github.com/chainreactors/aiscan/pkg/tools/neutron"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	spraycmd "github.com/chainreactors/aiscan/pkg/tools/spray"
	zombiecmd "github.com/chainreactors/aiscan/pkg/tools/zombie"
)

func RegisterAll(reg *ScannerRegistry, engineSet *engine.Set, opts ...scan.Option) error {
	return RegisterAllWithLogger(reg, engineSet, telemetry.NopLogger(), opts...)
}

func RegisterAllWithLogger(reg *ScannerRegistry, engineSet *engine.Set, logger telemetry.Logger, opts ...scan.Option) error {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if engineSet == nil {
		engineSet = &engine.Set{}
	}
	scanOpts := append([]scan.Option{scan.WithLogger(logger)}, opts...)
	if engineSet.Resources != nil {
		reg.Register(cyberhubcmd.New(engineSet.Resources))
	}
	if engineSet.Gogo != nil && engineSet.Spray != nil {
		reg.Register(scan.New(engineSet, scanOpts...))
	}
	if engineSet.Gogo != nil {
		reg.Register(gogocmd.New(engineSet.Gogo).WithLogger(logger))
	}
	if engineSet.Spray != nil {
		reg.Register(spraycmd.New(engineSet.Spray).WithLogger(logger))
	}
	if engineSet.Zombie != nil {
		reg.Register(zombiecmd.New(engineSet.Zombie).WithLogger(logger))
	}
	if engineSet.Neutron != nil {
		reg.Register(neutroncmd.New(engineSet.Neutron, engineSet.Index).WithLogger(logger))
	}

	logger.Infof("scanner commands=%s", fmt.Sprintf("%v", reg.Names()))
	return nil
}
