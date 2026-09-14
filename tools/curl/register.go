package curl

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func NewCommand(logger telemetry.Logger, proxy string, events aop.EventPublisher) commands.Command {
	impl := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/curl.md",
		Run:             impl.Run,
	}
}
