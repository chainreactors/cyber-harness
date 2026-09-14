//go:build full && record_ffmpeg && cgo && (windows || linux)

package main

import (
	"context"
	"slices"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/edition"
)

func TestRecordFullCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "browser", "core", "curl", "gogo", "ioa", "katana", "neutron", "passive", "proton", "proxy", "record", "scan", "search", "spray", "zombie"}
	if got := edition.Catalog().IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("record full capabilities = %#v, want %#v", got, want)
	}
}

func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	product, err := newAIScanProfile(aiscanProfileConfig{
		Option: &cfg.Option{},
		Application: apppkg.Config{
			Tools: apppkg.ToolConfig{BashTimeout: 1}, Logger: telemetry.NopLogger(), SkipEngines: true,
		},
		Logger: telemetry.NopLogger(),
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
