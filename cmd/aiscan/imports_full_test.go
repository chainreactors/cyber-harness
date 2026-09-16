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

func TestFullEditionBuildTags(t *testing.T) {
	assertEditionTags(t, "FULL")
	assertEditionCGO(t, "FULL")
}
