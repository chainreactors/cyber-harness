//go:build full && record && cgo && (windows || linux)

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	cyberdist "github.com/chainreactors/cyber/pkg/aiscan"
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

// The tool registry is a capability, so this borrows it the way an extension
// would instead of reading it off the application.
func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	graph, err := cyberdist.Extensions(cyberdist.AppConfig{Logger: telemetry.NopLogger(), SkipEngines: true}, agent.NoLoop(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tools tool.Executor
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		tools, err = extension.Use[tool.Executor](scope)
		return err
	}}
	set, err := extension.New(append(graph, borrow)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, definition := range tools.ToolDefinitions() {
		found = found || definition.Name == "record"
	}
	if !found {
		t.Fatal("record tool is linked but was not assembled by the runner")
	}
}
