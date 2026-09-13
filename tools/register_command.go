package tools

import (
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func NewScanCommand(engines *engine.Set, options []scan.Option, proxy string, events aop.EventEmitter) (commands.Command, error) {
	if engines == nil || engines.Gogo == nil || engines.Spray == nil {
		return commands.Command{}, fmt.Errorf("scan engines are unavailable")
	}
	scanOptions := append([]scan.Option(nil), options...)
	if proxy != "" {
		scanOptions = append(scanOptions, scan.WithProxy(proxy))
	}
	if events != nil {
		scanOptions = append(scanOptions, scan.WithEvents(events))
	}
	impl := scan.New(engines, scanOptions...)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/scan.md",
		Run:             impl.Run,
	}, nil
}
