//go:build full

package katana

import (
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func NewCommand(logger telemetry.Logger, proxy string, events aop.EventEmitter) commands.Command {
	impl := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/katana.md",
		Run:             impl.Run,
	}
}
