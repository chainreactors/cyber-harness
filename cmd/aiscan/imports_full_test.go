//go:build full && !record_ffmpeg

package main

import (
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/pkg/edition"
)

func TestFullCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "browser", "core", "curl", "gogo", "katana", "neutron", "passive", "proton", "proxy", "scan", "search", "spray", "zombie"}
	if got := edition.Catalog().IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("full capabilities = %#v, want %#v", got, want)
	}
}
