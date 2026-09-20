//go:build !full

package scanner

import (
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func manifestScannerCommands(*app.State, *engine.Set, telemetry.Logger, string) ([]coretool.Command, error) {
	return nil, nil
}
