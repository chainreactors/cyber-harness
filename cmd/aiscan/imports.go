package main

// Command registration via init() side effects.
// Each package has a register_command.go that calls command.RegisterFactory().

import (
	_ "github.com/chainreactors/aiscan/pkg/tools/ioa"
	_ "github.com/chainreactors/aiscan/pkg/tools"
	_ "github.com/chainreactors/aiscan/pkg/tools/cyberhub"
	_ "github.com/chainreactors/aiscan/pkg/command/results"
)
