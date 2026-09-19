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
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tmuxext "github.com/chainreactors/cyber/pkg/exts/tmux"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// NewState builds the shared application state owned by a test.
func NewState(t testing.TB, logger telemetry.Logger, stream *coreevents.Stream) *app.State {
	t.Helper()
	if stream == nil {
		stream = coreevents.New()
	}
	application, err := app.New(logger, stream)
	if err != nil {
		t.Fatal(err)
	}
	return application
}

// Entries returns the extensions a test host owns, in load order. It publishes
// the same capabilities a profile does -- hooks, events, the executors, the
// skill store, a routing endpoint that routes nothing, and the application
// itself -- so an extension under test borrows them exactly as it would in
// production.
func Entries(t testing.TB, application *app.State) []extension.Extension {
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
		extension.Provided[*hooks.Registry](registry),
		extension.Provided[*coreevents.Stream](stream),
		extension.Provided[*skills.Store](library),
		extension.Provided[egress.Endpoint](egress.Disabled()),
		extension.Provided[*app.State](application),
		commands.NewRegistry(),
		toolset.NewRegistry(),
		terminalext.New(terminalext.Config{Directory: t.TempDir(), Timeout: 1}),
		tmuxext.New(),
	}
}

// Load builds the host graph and loads it.
func Load(t testing.TB, ctx context.Context, application *app.State) *extension.Set {
	t.Helper()
	return hosttest.Load(t, ctx, Entries(t, application)...)
}
