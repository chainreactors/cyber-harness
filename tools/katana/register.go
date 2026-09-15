//go:build full

package katana

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
)

func NewCommand(logger telemetry.Logger, proxy string, events aop.EventPublisher) commands.Command {
	impl := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/katana.md",
		Run:             impl.Run,
	}
}
