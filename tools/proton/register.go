package proton

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/resources"
)

func NewCommand(workDir string, resources *resources.Set, logger telemetry.Logger, proxy string, events aop.EventPublisher) commands.Command {
	cmd := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	if resources != nil {
		cmd.WithResourceProvider(resources.ProtonConfig)
	}
	cmd.SetWorkDir(workDir)
	return commands.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/proton.md",
		Run:             cmd.Run,
	}
}
