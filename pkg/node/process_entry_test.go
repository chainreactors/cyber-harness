package node

import (
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if code, handled := terminaltool.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
