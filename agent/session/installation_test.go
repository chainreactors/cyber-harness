package session

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
)

// newUnitResource supplies explicit dependencies for package-internal resource tests.
// Product and integration callers install the real Session Extension.
func newUnitResource(t *testing.T, f *apptest.Fixture, c Config) (*Resource, error) {
	t.Helper()
	if f == nil {
		f = apptest.NewFixture(t, nil, nil)
	}
	if f.Hooks == nil {
		apptest.Load(t, t.Context(), f)
	}
	c.Providers, c.Events, c.Logger = f.Providers, f.Stream, f.Logger
	c.Hooks, c.Tools, c.CommandRegistry, c.Skills, c.Shell = f.Hooks, f.Tools, f.Commands, f.Skills, f.Shell
	if c.History == nil {
		c.History = JSONLHistory{}
	}
	if c.Loop == nil {
		c.Loop = agent.NoLoop()
	}
	return NewResource(c)
}

// Load only bridges the unit under test to the existing cleanup fixture.
func (r *Resource) Load(scope *extension.Scope) error { return r.Start(scope.Init(), scope.Lifetime()) }

var _ interface{ Close(context.Context) error } = (*Resource)(nil)
