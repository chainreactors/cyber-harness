//go:build !full

package main

import (
	"slices"
	"testing"

	"github.com/chainreactors/cyber/pkg/edition"
)

func TestDefaultCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "core", "curl", "gogo", "neutron", "proton", "proxy", "scan", "search", "spray", "zombie"}
	if got := edition.Catalog().IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("default capabilities = %#v, want %#v", got, want)
	}
}
