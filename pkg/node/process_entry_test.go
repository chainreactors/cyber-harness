package node

import (
	"github.com/chainreactors/cyber/pkg/commands"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if code, handled := commands.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
