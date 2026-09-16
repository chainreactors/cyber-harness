//go:build !full

package main

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/extension"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func editionExtensionEntries(*app.App, toolset.Runtime, commands.Runtime, app.Config, capability.Plan, string) ([]extension.Entry, error) {
	return nil, nil
}
