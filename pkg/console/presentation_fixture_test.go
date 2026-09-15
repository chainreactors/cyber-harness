package console

import (
	"context"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/console/api"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	"testing"
)

// Tests install the same provider/contributor path as a product profile.
func testSessionBindings(t *testing.T, catalog sessionconsole.Catalog) *api.Bindings {
	t.Helper()
	tui := tuiext.New()
	contribution, err := sessionconsole.New(tui.Registrar(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "tui", Extension: tui}, extension.Entry{ID: "session.repl", DependsOn: []string{"tui"}, Extension: contribution})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	return tui.Bindings()
}
