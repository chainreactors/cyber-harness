//go:build !full

package main

import (
	"slices"
	"testing"
)

func TestDefaultScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "neutron", "proton", "scan", "spray", "zombie"}
	got := scannerNames()
	if !slices.Equal(got, want) {
		t.Fatalf("default scanners = %#v, want %#v", got, want)
	}
}

func TestDefaultCLIExcludesFullScannerCommands(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range []string{"katana", "passive"} {
		if parser.Command.Find(name) != nil {
			t.Errorf("standard CLI unexpectedly declares full-only command %q", name)
		}
	}
}

// CGO is deliberately not asserted here: the standard manifest gates no cgo
// file, so STANDARD_CGO only keeps the release binary free of a C toolchain,
// while a test build may legitimately leave cgo at its default.
func TestStandardManifestTags(t *testing.T) {
	assertManifestTags(t, "STANDARD")
}
