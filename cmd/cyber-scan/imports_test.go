//go:build !record

package main

import (
	"slices"
	"testing"
)

func TestScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "katana", "neutron", "passive", "proton", "scan", "spray", "zombie"}
	got := productScannerNames()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("scanners = %#v, want %#v", got, want)
	}
}

func TestCLIDeclaresScannerCommands(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range []string{"katana", "passive"} {
		if parser.Find(name) == nil {
			t.Errorf("CLI is missing command %q", name)
		}
	}
}

func TestScannerManifestTags(t *testing.T) {
	assertManifestTags(t, "STANDARD")
}
