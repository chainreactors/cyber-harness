//go:build full && record && cgo && (windows || linux)

package main

import (
	"context"
	"slices"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
)

func TestRecordFullScannerSet(t *testing.T) {
	want := []string{"curl", "gogo", "katana", "neutron", "passive", "proton", "scan", "spray", "zombie"}
	if got := scannerNames(); !slices.Equal(got, want) {
		t.Fatalf("record full scanners = %#v, want %#v", got, want)
	}
}

func TestRecordManifestTags(t *testing.T) {
	assertManifestTags(t, "RECORD")
	assertManifestCGO(t, "RECORD")
}

func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	p, err := newCyberProfile(cyberProfileConfig{
		Option: &cfg.Option{},
		Application: appConfig{
			Logger: telemetry.NopLogger(), SkipEngines: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	application, err := p.App()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	if application.Tools != nil {
		for _, definition := range application.Tools.ToolDefinitions() {
			found = found || definition.Name == "record"
		}
	}
	if !found {
		t.Fatal("record tool is linked but was not assembled by the runner")
	}
}
