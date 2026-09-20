//go:build !full

package scanner

import (
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func manifestScannerCommands(aop.EventPublisher, *engine.Set, telemetry.Logger, string) ([]coretool.Command, error) {
	return nil, nil
}
