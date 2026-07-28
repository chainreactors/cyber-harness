package main

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
	cfg "github.com/chainreactors/aiscan/core/config"
)

func TestAgentCapabilitySetHasNoScanner(t *testing.T) {
	want := []string{"arsenal", "core", "ioa"}
	if got := capability.IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("agent capabilities = %#v, want %#v", got, want)
	}
	for _, descriptor := range capability.All() {
		if descriptor.Kind == capability.KindScanner {
			t.Fatalf("agent linked scanner capability %q", descriptor.ID)
		}
	}
	if got := cfg.CLICommandSummary(); got != "agent, web, serve" {
		t.Fatalf("agent command summary = %q, want %q", got, "agent, web, serve")
	}
}
