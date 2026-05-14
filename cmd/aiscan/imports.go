package main

// Command registration via init() side effects.
// Each package has a register_command.go that calls command.RegisterFactory().

import (
	_ "github.com/chainreactors/aiscan/pkg/ioacmd"
	_ "github.com/chainreactors/aiscan/pkg/scanner"
	_ "github.com/chainreactors/aiscan/pkg/scanner/cyberhub"
	_ "github.com/chainreactors/aiscan/pkg/command/results"
)
