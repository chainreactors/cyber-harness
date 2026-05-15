package main

import (
	"os"

	"github.com/chainreactors/aiscan/cmd"
)

func main() {
	os.Args = append([]string{os.Args[0], "ioa"}, os.Args[1:]...)
	cmd.AiScan()
}
