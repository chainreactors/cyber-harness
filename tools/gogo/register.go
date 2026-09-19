package gogo

import (
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger, proxy string, events aop.EventPublisher) (commands.Command, error) {
	if engines == nil || engines.Gogo == nil {
		return commands.Command{}, fmt.Errorf("gogo engine is unavailable")
	}
	impl := New(engines.Gogo).WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/gogo.md",
		Run:             impl.Run,
	}, nil
}
