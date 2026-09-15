package service

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/hooks"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// newTestApp supplies explicitly test-owned, unpublished registries. Tests that
// execute commands or tools must activate their registry in their own graph.
func newTestApp(t testing.TB, config apppkg.Config, deps apppkg.AppServices) *apppkg.Resource {
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
	resource, err := apppkg.New(config, deps)
	if err != nil {
		t.Fatal(err)
	}
	return resource
}
