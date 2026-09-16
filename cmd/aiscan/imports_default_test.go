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

// CGO is deliberately not asserted here: the standard edition gates no cgo
// file, so STANDARD_CGO only keeps the release binary free of a C toolchain,
// while a test build may legitimately leave cgo at its default.
func TestStandardEditionBuildTags(t *testing.T) {
	assertEditionTags(t, "STANDARD")
}
