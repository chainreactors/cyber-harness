package curl

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
)

func NewCommand(logger telemetry.Logger, proxy string, events aop.EventPublisher) commands.Command {
	impl := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/curl.md",
		Run:             impl.Run,
	}
}
