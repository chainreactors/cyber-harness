package neutron

import (
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger, proxy string, events aop.EventPublisher) (commands.Command, error) {
	if engines == nil || engines.Neutron == nil {
		return commands.Command{}, fmt.Errorf("neutron engine is unavailable")
	}
	impl := New(engines.Neutron, engines.Index).WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/neutron.md",
		Run:             impl.Run,
	}, nil
}
