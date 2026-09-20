package neutron

import (
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger, proxy string, events aop.EventPublisher) (coretool.Command, error) {
	if engines == nil || engines.Neutron == nil {
		return coretool.Command{}, fmt.Errorf("neutron engine is unavailable")
	}
	impl := New(engines.Neutron, engines.Index).WithLogger(logger).WithProxy(proxy).WithEvents(events)
	return coretool.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/neutron.md",
		Run:             impl.Run,
	}, nil
}
