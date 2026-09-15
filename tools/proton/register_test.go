package proton_test

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/cmd/harness"
	protoncmd "github.com/chainreactors/aiscan/tools/proton"
)

func TestFactoryBuildsProtonWithScannerGroup(t *testing.T) {
	registry := harness.Commands(t, "scanner", protoncmd.NewCommand(t.TempDir(), nil, nil, "", nil))

	if !registry.Has("proton") {
		t.Fatal("scanner group did not register proton")
	}
	if got := registry.GroupNames("scanner"); !slices.Contains(got, "proton") {
		t.Fatalf("scanner group = %#v, want proton", got)
	}
}
