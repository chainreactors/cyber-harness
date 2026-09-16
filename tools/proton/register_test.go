package proton_test

import (
	"testing"

	"github.com/chainreactors/cyber/cmd/harness"
	protoncmd "github.com/chainreactors/cyber/tools/proton"
)

func TestFactoryBuildsProtonCommand(t *testing.T) {
	registry := harness.Commands(t, protoncmd.NewCommand(t.TempDir(), nil, nil, "", nil))

	if !registry.Has("proton") {
		t.Fatal("proton command was not registered")
	}
}
