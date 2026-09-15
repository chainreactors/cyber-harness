//go:build full

package browser

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/cmd/harness"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
)

func TestModuleOwnsBrowserRegistration(t *testing.T) {
	registry := commands.NewRegistry(nil)
	instance, err := New(registry, t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	set := harness.Set(t,
		extension.Entry{ID: "browser", Extension: instance},
		extension.Entry{ID: "commands", DependsOn: []string{"browser"}, Extension: registry},
	)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !registry.Has("playwright") {
		t.Fatal("browser command was not published")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Has("playwright") {
		t.Fatal("browser command remained published")
	}
}
