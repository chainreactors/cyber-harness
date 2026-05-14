package main

// Build-tag-controlled command registration.
// Each imported package has a register_command.go with a build tag guard.
// The init() inside only runs when the corresponding tag is active.

import (
	_ "github.com/chainreactors/aiscan/pkg/ioacmd"
	_ "github.com/chainreactors/aiscan/pkg/scanner"
	_ "github.com/chainreactors/aiscan/pkg/scanner/cyberhub"
	_ "github.com/chainreactors/aiscan/pkg/tool/results"
)
