package console

import (
	"context"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/console/api"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	"testing"
)

// Tests install the same provider/contributor path as a Profile.
func testSessionBindings(t *testing.T, runtime *agentsession.Runtime) *api.Bindings {
	t.Helper()
	tui := tuiext.New()
	contribution := sessionconsole.New()
	var registry *api.Registry
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		registry, err = extension.Use[*api.Registry](scope)
		return err
	}}
	set, err := extension.New(extension.Provided[*agentsession.Runtime](runtime), tui, contribution, borrow)
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
	return registry.Bindings()
}
