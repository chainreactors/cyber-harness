//go:build !full

package scanner

import (
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func manifestScannerCommands(*app.App, *engine.Set, telemetry.Logger, string) ([]commands.Command, error) {
	return nil, nil
}

func manifestScanOptions() []scan.Option { return nil }
