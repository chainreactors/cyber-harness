package proton

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/resources"
)

func NewCommand(workDir string, resources *resources.Set, logger telemetry.Logger, proxy string, events aop.EventPublisher) coretool.Command {
	cmd := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	if resources != nil {
		cmd.WithResourceProvider(resources.ProtonConfig)
	}
	cmd.SetWorkDir(workDir)
	return coretool.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/proton.md",
		Run:             cmd.Run,
	}
}
