//go:build full

package scanner

import (
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func manifestScannerCommands(application *app.State, engines *engine.Set, logger telemetry.Logger, proxyURL string) ([]coretool.Command, error) {
	return []coretool.Command{katana.NewCommand(logger, proxyURL, application), passive.NewCommand(engines, logger)}, nil
}
