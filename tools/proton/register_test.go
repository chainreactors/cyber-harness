package proton

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestFactoryBuildsProtonWithScannerGroup(t *testing.T) {
	registry := commands.NewRegistry()
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"scanner"}}), &commands.Deps{WorkDir: t.TempDir()}, registry)

	if !registry.Has("proton") {
		t.Fatal("scanner group did not register proton")
	}
	if got := registry.GroupNames("scanner"); !slices.Contains(got, "proton") {
		t.Fatalf("scanner group = %#v, want proton", got)
	}
}
