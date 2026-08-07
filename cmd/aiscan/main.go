package main

import (
	"os"

	"github.com/chainreactors/aiscan/pkg/commands"
)

func main() {
	if code, ok := commands.RunCommandBridgeProxyIfRequested(); ok {
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	aiscan()
}
