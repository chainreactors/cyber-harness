package app

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// The harness imports this package, so these tests build their own host instead
// of importing the harness.
func testSet(t testing.TB, entries ...extension.Extension) *extension.Set {
	t.Helper()
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return set
}

// newTestApp supplies explicitly test-owned, unpublished registries. Tests that
// execute commands or tools must activate their registry in their own graph.
func newTestApp(t testing.TB, logger telemetry.Logger, deps Dependencies) *App {
	t.Helper()
	if deps.Hooks == nil {
		deps.Hooks = hooks.New()
	}
	if deps.Events == nil {
		deps.Events = events.New()
	}
	if deps.Commands == nil {
		registry := commands.NewRegistry(deps.Hooks)
		deps.Commands = registry
		t.Cleanup(func() {
			if err := registry.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	if deps.Tools == nil {
		registry := toolset.NewRegistry(deps.Hooks)
		deps.Tools = registry
		t.Cleanup(func() {
			if err := registry.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	application, err := New(logger, deps)
	if err != nil {
		t.Fatal(err)
	}
	return application
}
