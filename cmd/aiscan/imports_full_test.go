//go:build full

package main

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
)

func TestFullCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "browser", "core", "gogo", "ioa", "katana", "neutron", "passive", "proton", "proxy", "scan", "search", "spray", "zombie"}
	if got := capability.IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("full capabilities = %#v, want %#v", got, want)
	}
}
