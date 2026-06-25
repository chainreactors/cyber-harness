package main

// Command registration via init() side effects.
// Each package has a register.go that calls command.RegisterFactory().

import (
	_ "github.com/chainreactors/aiscan/pkg/tools"
	_ "github.com/chainreactors/aiscan/pkg/tools/arsenal"
	_ "github.com/chainreactors/aiscan/pkg/tools/gogo"
	_ "github.com/chainreactors/aiscan/pkg/tools/ioa"
	_ "github.com/chainreactors/aiscan/pkg/tools/neutron"
	_ "github.com/chainreactors/aiscan/pkg/tools/proton"
	_ "github.com/chainreactors/aiscan/pkg/tools/proxy"
	_ "github.com/chainreactors/aiscan/pkg/tools/search"
	_ "github.com/chainreactors/aiscan/pkg/tools/spray"
	_ "github.com/chainreactors/aiscan/pkg/tools/zombie"
)
