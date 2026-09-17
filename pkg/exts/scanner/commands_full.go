//go:build full

package scanner

import (
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func manifestScannerCommands(application *app.App, engines *engine.Set, logger telemetry.Logger, proxyURL string) ([]commands.Command, error) {
	return []commands.Command{katana.NewCommand(logger, proxyURL, application), passive.NewCommand(engines, logger)}, nil
}

func manifestScanOptions() []scan.Option { return scan.KatanaOptions() }
