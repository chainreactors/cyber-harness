//go:build !full

package main

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func editionExtensionEntries(*app.App, toolset.Runtime, commands.Runtime, app.Config, capability.Plan, string) ([]extension.Entry, error) {
	return nil, nil
}
