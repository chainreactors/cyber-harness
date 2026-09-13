package proton

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/internal/extensiontest"
)

func TestFactoryBuildsProtonWithScannerGroup(t *testing.T) {
	registry := extensiontest.Commands(t, "scanner", NewCommand(t.TempDir(), nil, nil, "", nil))

	if !registry.Has("proton") {
		t.Fatal("scanner group did not register proton")
	}
	if got := registry.GroupNames("scanner"); !slices.Contains(got, "proton") {
		t.Fatalf("scanner group = %#v, want proton", got)
	}
}
