package proton

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func NewCommand(workDir string, resources *resources.Set, logger telemetry.Logger, proxy string, events aop.EventEmitter) commands.Command {
	cmd := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	if resources != nil {
		cmd.WithResourceProvider(resources.ProtonConfig)
	}
	cmd.SetWorkDir(workDir)
	return commands.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/proton.md",
		Run:             cmd.Run,
	}
}
