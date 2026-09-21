//go:build full

package browser

import (
	"context"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	"testing"
)

func TestModuleOwnsBrowserRegistration(t *testing.T) {
	registry := coretool.NewCommandRegistry()
	instance, err := New(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	set := hosttest.Set(t,
		hosttest.Capabilities(),
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
