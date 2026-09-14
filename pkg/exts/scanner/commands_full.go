//go:build full

package scanner

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/katana"
	"github.com/chainreactors/aiscan/tools/passive"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionScannerCommands(application *app.App, plan capability.Plan, engines *engine.Set, logger telemetry.Logger, proxyURL string) ([]commands.Command, error) {
	var result []commands.Command
	if plan.Has("katana") {
		result = append(result, katana.NewCommand(logger, proxyURL, application))
	}
	if plan.Has("passive") {
		result = append(result, passive.NewCommand(engines, logger))
	}
	return result, nil
}

func editionScanOptions() []scan.Option { return scan.KatanaOptions() }
