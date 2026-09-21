//go:build full

package katana

import (
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func NewCommand(logger telemetry.Logger, proxy string, events aop.EventPublisher) coretool.Command {
	impl := New().WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return coretool.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/katana.md",
		Run:             impl.Run,
	}
}
