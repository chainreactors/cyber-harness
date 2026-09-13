package spray

import (
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger, proxy string, events aop.EventEmitter) (commands.Command, error) {
	if engines == nil || engines.Spray == nil {
		return commands.Command{}, fmt.Errorf("spray engine is unavailable")
	}
	impl := New(engines.Spray).WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/spray.md",
		Run:             impl.Run,
	}, nil
}
