//go:build full

package scanner

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
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
