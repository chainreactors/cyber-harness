package main

import (
	"slices"
	"testing"
)

// The record section and the record extension must appear and disappear
// together. Without the build tag on its declaration, every default build
// validated extensions.record.max_concurrent, wrote it into --init output and
// showed it in settings, while no extension could exist to read it.
func TestRecordSectionTracksTheRecordExtension(t *testing.T) {
	declared := slices.Contains(defaultSections().Aliases(), "record")
	if declared != recordExtensionLinked {
		t.Fatalf("record section declared=%v but the extension linked=%v", declared, recordExtensionLinked)
	}
}
