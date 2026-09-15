package main

import (
	"github.com/chainreactors/cyber/pkg/commands"
	"os"
)

func main() {
	if code, handled := commands.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	cyber()
}
