package proton

import (
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestFactoryBuildsProtonWithScannerGroup(t *testing.T) {
	registry := commands.NewRegistry()
	commands.BuildGroup("scanner", &commands.Deps{WorkDir: t.TempDir()}, registry)

	if !registry.Has("proton") {
		t.Fatal("scanner group did not register proton")
	}
	if got := registry.GroupNames("scanner"); len(got) != 1 || got[0] != "proton" {
		t.Fatalf("scanner group = %#v, want [proton]", got)
	}
}
