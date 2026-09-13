package zombie

import (
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger, proxy string, events aop.EventEmitter) (commands.Command, error) {
	if engines == nil || engines.Zombie == nil {
		return commands.Command{}, fmt.Errorf("zombie engine is unavailable")
	}
	impl := New(engines.Zombie).WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/zombie.md",
		Run:             impl.Run,
	}, nil
}
