//go:build !full

package main

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
)

func TestDefaultCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "core", "gogo", "ioa", "neutron", "proton", "proxy", "scan", "search", "spray", "zombie"}
	if got := capability.IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("default capabilities = %#v, want %#v", got, want)
	}
}
