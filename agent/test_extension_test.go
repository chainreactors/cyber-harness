package agent

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

// The harness imports pkg/app, which imports this package, so these tests build
// their own hosts instead of importing the harness.

func testSet(t testing.TB, entries ...extension.Entry) *extension.Set {
	t.Helper()
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return set
}

func testCommands(t testing.TB, group string, values ...commands.Command) *commands.Registry {
	t.Helper()
	registry := commands.NewRegistry(nil)
	if err := registry.Register("fixture", group, values...); err != nil {
		t.Fatal(err)
	}
	set := testSet(t, extension.Entry{ID: "command-registry", Extension: registry})
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return registry
}

func testTools(t testing.TB, values ...coretool.Tool) coretool.Executor {
	t.Helper()
	return testToolsWithHooks(t, nil, values...)
}

func testToolsWithHooks(t testing.TB, registry *hooks.Registry, values ...coretool.Tool) coretool.Executor {
	t.Helper()
	if len(values) == 0 {
		return coretool.EmptyExecutor()
	}
	toolRegistry := toolset.NewRegistry(registry)
	if err := toolRegistry.Register("fixture", values...); err != nil {
		t.Fatal(err)
	}
	set := testSet(t, extension.Entry{ID: "tool-registry", Extension: toolRegistry})
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return toolRegistry
}
