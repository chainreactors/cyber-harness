//go:build full

package main

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	browserext "github.com/chainreactors/aiscan/pkg/exts/browser"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/katana"
	"github.com/chainreactors/aiscan/tools/passive"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionExtensionEntries(application *app.App, tools *toolset.Registry, commands *commands.Registry, config app.Config, plan capability.Plan, workDir string) ([]extension.Entry, error) {
	var entries []extension.Entry
	if plan.Has("browser") {
		browser, err := browserext.New(commands, workDir, config.Tools.PlaywrightSession)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "browser", Extension: browser})
	}
	return appendRecorderEntry(entries, application, tools, plan, workDir)
}

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
