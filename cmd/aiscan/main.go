package main

import (
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"os"
)

func main() {
	if code, handled := terminaltool.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	cyber()
}
