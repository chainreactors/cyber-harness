//go:build !full

package scanner

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionScannerCommands(*app.App, capability.Plan, *engine.Set, telemetry.Logger, string) ([]commands.Command, error) {
	return nil, nil
}

func editionScanOptions() []scan.Option { return nil }
