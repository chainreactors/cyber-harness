//go:build full

package main

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	browserext "github.com/chainreactors/aiscan/pkg/exts/browser"
	"github.com/chainreactors/aiscan/pkg/toolset"
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
