package main

import (
	"github.com/chainreactors/aiscan/pkg/commands"
	"os"
)

func main() {
	if code, handled := commands.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	aiscan()
}
