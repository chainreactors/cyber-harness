//go:build full && !record

package main

import (
	"slices"
	"testing"
)

func TestFullScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "katana", "neutron", "passive", "proton", "scan", "spray", "zombie"}
	if got := scannerNames(); !slices.Equal(got, want) {
		t.Fatalf("full scanners = %#v, want %#v", got, want)
	}
}

func TestFullCLIDeclaresFullScannerCommands(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range []string{"katana", "passive"} {
		if parser.Find(name) == nil {
			t.Errorf("full CLI is missing command %q", name)
		}
	}
}

func TestFullManifestTags(t *testing.T) {
	assertManifestTags(t, "FULL")
}
