//go:build full

package browser

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/hosttest"
)

func TestModuleOwnsBrowserRegistration(t *testing.T) {
	registry := commands.NewRegistry()
	instance, err := New(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	set := hosttest.Set(t,
		registry,
		instance,
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
