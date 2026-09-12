//go:build full

package browser

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/internal/extensiontest"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestModuleOwnsBrowserRegistration(t *testing.T) {
	registry := commands.NewRegistry()
	instance, err := New(registry, t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	set := extensiontest.Load(t, t.Context(), instance)
	if !registry.Has("playwright") {
		t.Fatal("browser command was not published")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Has("playwright") {
		t.Fatal("browser command remained published")
	}
	if err := registry.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
