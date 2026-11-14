//go:build record && cgo && (windows || linux)

package main

import (
	"slices"
	"testing"
)

func TestRecordScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "katana", "neutron", "passive", "proton", "scan", "spray", "zombie"}
	got := productScannerNames()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("record scanners = %#v, want %#v", got, want)
	}
}

func TestRecordManifestTags(t *testing.T) {
	assertManifestTags(t, "RECORD")
	assertManifestCGO(t, "RECORD")
}
