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

func TestRecordEditionBuildTags(t *testing.T) {
	assertEditionTags(t, "RECORD")
	assertEditionCGO(t, "RECORD")
}

func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	product, err := newCyberProfile(cyberProfileConfig{
		Option: &cfg.Option{},
		Application: applicationConfig{
			Logger: telemetry.NopLogger(), SkipEngines: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := product.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = product.Close(context.Background()) })
	application, err := product.App()
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
