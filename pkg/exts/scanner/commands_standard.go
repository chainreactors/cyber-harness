//go:build !full

package scanner

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func editionScannerCommands(*app.App, capability.Plan, *engine.Set, telemetry.Logger, string) ([]commands.Command, error) {
	return nil, nil
}

func editionScanOptions() []scan.Option { return nil }
