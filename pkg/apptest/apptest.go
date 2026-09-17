// Package apptest builds a minimal App host graph owned by a single test.
//
// It is hosttest plus the App-level wiring, kept separate so that packages App
// itself depends on can still use hosttest without an import cycle.
// Production code must construct an explicit Profile instead.
package apptest

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	app "github.com/chainreactors/cyber/pkg/app"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// Entries returns the extensions a test App host owns. A test that supplies no
// Bash gets a terminal built here, and the returned owner closes whichever of
// the two the test ended up with.
func Entries(t testing.TB, application *app.App, _ ...string) []extension.Extension {
	t.Helper()
	if application == nil {
		t.Fatal("test application is required")
	}
	tools, ok := application.Tools.(*toolset.Registry)
	if !ok {
		t.Fatal("test application does not expose its concrete tool registry")
	}
	var terminalOwner extension.Extension = extension.Func{CloseFunc: func(context.Context) error {
		if application.Bash != nil {
			application.Bash.Close()
		}
		return nil
	}}
	if application.Bash == nil {
		terminal, err := terminalext.New(application.Hooks, application.Commands, terminalext.Config{Directory: t.TempDir(), Timeout: 1})
		if err != nil {
			t.Fatal(err)
		}
		application.Bash = terminal.Bash()
		terminalOwner = terminal
	}
	return []extension.Extension{
		application.Commands.(extension.Extension),
		tools,
		terminalOwner,
	}
}

// Load builds the App host graph and loads it.
func Load(t testing.TB, ctx context.Context, application *app.App, dependencies ...string) *extension.Set {
	t.Helper()
	return hosttest.Load(t, ctx, Entries(t, application, dependencies...)...)
}
