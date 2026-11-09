//go:build !full

package main

import (
	"slices"
	"testing"
)

func TestDefaultScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "neutron", "proton", "scan", "spray", "zombie"}
	got := productScannerNames()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("default scanners = %#v, want %#v", got, want)
	}
}

func TestDefaultCLIExcludesFullScannerCommands(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range []string{"katana", "passive"} {
		if parser.Find(name) != nil {
			t.Errorf("standard CLI unexpectedly declares full-only command %q", name)
		}
	}
}

// CGO is deliberately not asserted here: the standard manifest gates no cgo
// file, so STANDARD_CGO only keeps the release binary free of a C toolchain,
// while a test build may legitimately leave cgo at its default.
func TestStandardManifestTags(t *testing.T) {
	if recordExtensionLinked {
		// Native record can be composed without the full discovery stack.
		// This custom composition is exactly STANDARD plus record.
		assertManifestTags(t, "STANDARD", "record")
		assertManifestCGO(t, "RECORD")
		return
	}
	assertManifestTags(t, "STANDARD")
}
