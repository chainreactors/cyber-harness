package agent

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// The harness imports pkg/app, which imports this package, so these tests build
// their own hosts instead of importing the harness.

func testSet(t testing.TB, entries ...extension.Extension) *extension.Set {
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

func testCommands(t testing.TB, values ...commands.Command) *commands.Registry {
	t.Helper()
	registry := commands.NewRegistry(nil)
	contributor := extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add(scope, values...) }}
	set := testSet(t, registry, contributor)
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
	contributor := extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add(scope, values...) }}
	set := testSet(t, toolRegistry, contributor)
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return toolRegistry
}
