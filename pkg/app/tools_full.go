//go:build full

package app

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	browserext "github.com/chainreactors/aiscan/pkg/exts/browser"
	"github.com/chainreactors/aiscan/tools/katana"
	"github.com/chainreactors/aiscan/tools/passive"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionToolEntries(app *App, config Config, plan capability.Plan) ([]extension.Entry, error) {
	var entries []extension.Entry
	if plan.Has("browser") {
		browser, err := browserext.New(app.Commands, app.workDir, config.Tools.PlaywrightSession)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "browser", Extension: browser})
	}
	return appendRecorderEntry(entries, app, plan)
}

func registerEditionScanners(app *App, plan capability.Plan, engines *engine.Set, logger telemetry.Logger) error {
	if plan.Has("katana") {
		if err := katana.Register(app.Commands, logger, app.proxyURL, app); err != nil {
			return err
		}
	}
	if plan.Has("passive") {
		if err := passive.Register(app.Commands, engines, logger); err != nil {
			return err
		}
	}
	return nil
}
