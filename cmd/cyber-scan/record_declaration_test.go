package main

import (
	"context"
	"slices"
	"testing"

	provider "github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
)

// The record section and the record extension must appear and disappear
// together. Without the build tag on its declaration, every default build
// validated extensions.record.max_concurrent, wrote it into init output and
// showed it in settings, while no extension could exist to read it.
func TestRecordSectionTracksTheRecordExtension(t *testing.T) {
	declared := slices.Contains(defaultSections().Aliases(), "record")
	if declared != recordExtensionLinked {
		t.Fatalf("record section declared=%v but the extension linked=%v", declared, recordExtensionLinked)
	}
}

// The distribution profile is the composition boundary. Its runtime exposes
// the tool capability assembled by the selected extensions.
func TestRecordRunnerBuildsDefaultRecordTool(t *testing.T) {
	if !recordExtensionLinked {
		t.Skip("native record extension is not linked")
	}
	option := &cfg.Option{}
	option.DataDir = t.TempDir()
	profile, err := newCyberScanProfile(profilepkg.Request{
		Option:       option,
		ProviderMode: provider.StartupDisabled,
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
