// Package apptest builds a minimal host graph owned by a single test.
//
// It is hosttest plus the capabilities an application-level extension expects,
// kept separate so that packages App itself depends on can still use hosttest
// without an import cycle. Production code must construct an explicit Profile
// instead.
package apptest

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/egress"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// Entries returns the extensions a test host owns, in load order. It publishes
// the same capabilities a profile does -- hooks, events, the executors, the
// skill store, a routing endpoint that routes nothing, and the application
// itself -- so an extension under test borrows them exactly as it would in
// production.
func Entries(t testing.TB, application *app.App, _ ...string) []extension.Extension {
	t.Helper()
	if application == nil {
		t.Fatal("test application is required")
	}
	registry := hooks.New()
	stream := application.Events()
	if stream == nil {
		stream = coreevents.New()
	}
	library := skills.NewStore(nil)

	return []extension.Extension{
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			if err := extension.Provide[*hooks.Registry](scope, registry); err != nil {
				return err
			}
			if err := extension.Provide[*coreevents.Stream](scope, stream); err != nil {
				return err
			}
			if err := extension.Provide[*skills.Store](scope, library); err != nil {
				return err
			}
			if err := extension.Provide[egress.Endpoint](scope, egress.Disabled()); err != nil {
				return err
			}
			return extension.Provide[*app.App](scope, application)
		}},
		commands.NewRegistry(registry),
		toolset.NewRegistry(registry),
		terminalext.New(terminalext.Config{Directory: t.TempDir(), Timeout: 1}),
	}
}

// Load builds the host graph and loads it.
func Load(t testing.TB, ctx context.Context, application *app.App, dependencies ...string) *extension.Set {
	t.Helper()
	return hosttest.Load(t, ctx, Entries(t, application, dependencies...)...)
}
