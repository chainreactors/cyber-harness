//go:build full && record_ffmpeg && cgo && (windows || linux)

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
)

func TestRecordFullCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "browser", "core", "gogo", "ioa", "katana", "neutron", "passive", "proton", "proxy", "record", "scan", "search", "spray", "zombie"}
	if got := capability.IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("record full capabilities = %#v, want %#v", got, want)
	}
}

func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	app, err := runner.NewApp(context.Background(), runner.ApplicationConfig{
		Tools:       runner.ToolConfig{BashTimeout: 1},
		Logger:      telemetry.NopLogger(),
		SkipEngines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	if _, ok := app.Commands.GetTool("record"); !ok {
		t.Fatal("record tool is linked but was not assembled by the runner")
	}
}
