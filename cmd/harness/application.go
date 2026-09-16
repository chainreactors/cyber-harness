package harness

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	app "github.com/chainreactors/cyber/pkg/app"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// AppEntries supplies a minimal App host graph for tests. Production code must
// construct a concrete Profile instead.
func AppEntries(t testing.TB, application *app.App, _ ...string) []extension.Extension {
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

func AppLoad(t testing.TB, ctx context.Context, application *app.App, dependencies ...string) *extension.Set {
	t.Helper()
	set := Set(t, AppEntries(t, application, dependencies...)...)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return set
}
