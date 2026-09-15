//go:build full

package main

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/extension"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	browserext "github.com/chainreactors/cyber/pkg/exts/browser"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func editionExtensionEntries(application *app.App, tools toolset.Runtime, commands commands.Runtime, config app.Config, plan capability.Plan, workDir string) ([]extension.Entry, error) {
	var entries []extension.Entry
	if plan.Has("browser") {
		browser, err := browserext.New(commands, workDir, config.Tools.PlaywrightSession)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "browser", Extension: browser})
	}
	return appendRecorderEntry(entries, config, tools, plan, workDir)
}
