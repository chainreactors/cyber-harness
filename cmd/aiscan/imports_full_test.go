//go:build full

package main

import (
	"context"
	"runtime"
	"slices"
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
)

func TestFullCapabilitySet(t *testing.T) {
	want := []string{"arsenal", "browser", "core", "gogo", "ioa", "katana", "neutron", "passive", "proton", "proxy", "scan", "search", "spray", "zombie"}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		want = append(want, "record")
		slices.Sort(want)
	}
	if got := capability.IDsSorted(); !slices.Equal(got, want) {
		t.Fatalf("full capabilities = %#v, want %#v", got, want)
	}
}

func TestFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("record is only linked on Windows and Linux")
	}
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
