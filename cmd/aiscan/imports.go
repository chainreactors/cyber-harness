package main

// Command registration via init() side effects.
// Each package has a register.go that calls command.RegisterFactory().

import (
	_ "github.com/chainreactors/aiscan/tools"
	_ "github.com/chainreactors/aiscan/tools/arsenal"
	_ "github.com/chainreactors/aiscan/tools/gogo"
	_ "github.com/chainreactors/aiscan/tools/ioa"
	_ "github.com/chainreactors/aiscan/tools/neutron"
	_ "github.com/chainreactors/aiscan/tools/proton"
	_ "github.com/chainreactors/aiscan/tools/proxy"
	_ "github.com/chainreactors/aiscan/tools/search"
	_ "github.com/chainreactors/aiscan/tools/spray"
	_ "github.com/chainreactors/aiscan/tools/zombie"
)
