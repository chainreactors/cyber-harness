//go:build !full

package main

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionExtensionEntries(*app.App, *toolset.Registry, *commands.Registry, app.Config, capability.Plan, string) ([]extension.Entry, error) {
	return nil, nil
}

func editionScannerCommands(*app.App, capability.Plan, *engine.Set, telemetry.Logger, string) ([]commands.Command, error) {
	return nil, nil
}
