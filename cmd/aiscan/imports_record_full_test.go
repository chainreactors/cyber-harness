//go:build full && record && cgo && (windows || linux)

package main

import (
	"context"
	"slices"
	"testing"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
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

// The distribution profile is the composition boundary. Its runtime exposes
// the tool capability assembled by the selected extensions.
func TestRecordFullRunnerBuildsDefaultRecordTool(t *testing.T) {
	option := &cfg.Option{}
	option.DataDir = t.TempDir()
	profile, err := newCyberProfileFromRequest(profilepkg.Request{
		Option:       option,
		ProviderMode: profilepkg.ProviderDisabled,
		Session:      &agentsession.Config{},
		Logger:       telemetry.NopLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = profile.Close(context.Background()) })
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, err := profile.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, definition := range runtime.Tools().ToolDefinitions() {
		found = found || definition.Name == "record"
	}
	if !found {
		t.Fatal("record tool is linked but was not assembled by the runner")
	}
}
