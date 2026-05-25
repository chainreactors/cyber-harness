package main

// Command registration via init() side effects for the root entrypoint.
// Keep this in sync with cmd/aiscan/imports.go.

import (
	_ "github.com/chainreactors/aiscan/pkg/command/results"
	_ "github.com/chainreactors/aiscan/pkg/tools"
	_ "github.com/chainreactors/aiscan/pkg/tools/cyberhub"
	_ "github.com/chainreactors/aiscan/pkg/tools/ioa"
	_ "github.com/chainreactors/aiscan/pkg/tools/vision"
	_ "github.com/chainreactors/aiscan/pkg/tools/webfetch"
	_ "github.com/chainreactors/aiscan/pkg/tools/websearch"
)
