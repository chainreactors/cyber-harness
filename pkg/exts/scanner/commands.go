package scanner

import (
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"

	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func newScanCommand(engines *engine.Set, options []scan.Option, proxy string, events aop.EventPublisher) (coretool.Command, error) {
	if engines == nil || engines.Gogo == nil || engines.Spray == nil {
		return coretool.Command{}, fmt.Errorf("scan engines are unavailable")
	}
	scanOptions := append([]scan.Option(nil), options...)
	if proxy != "" {
		scanOptions = append(scanOptions, scan.WithProxy(proxy))
	}
	if events != nil {
		scanOptions = append(scanOptions, scan.WithEvents(events))
	}
	impl := scan.New(engines, scanOptions...)
	return coretool.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/scan.md",
		Run:             impl.Run,
	}, nil
}
