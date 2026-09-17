// Package hosttest builds extension hosts owned by a single test. It exists so
// that package tests do not each re-implement the same three-line host wiring.
//
// Production code must construct an explicit Profile instead. The guard test in
// this package fails if any non-test package links it.
package hosttest

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// Set builds an extension set closed when the test ends.
func Set(t testing.TB, values ...extension.Extension) *extension.Set {
	t.Helper()
	set, err := extension.New(values...)
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

// Load builds a set and loads it, reporting either failure.
func Load(t testing.TB, ctx context.Context, values ...extension.Extension) *extension.Set {
	t.Helper()
	set := Set(t, values...)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return set
}

// contribute adds values to the scope of a host being loaded.
func contribute[T any](values ...T) extension.Extension {
	return extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add(scope, values...) }}
}

// Commands returns a loaded registry holding the given commands.
func Commands(t testing.TB, values ...commands.Command) *commands.Registry {
	t.Helper()
	registry := commands.NewRegistry(nil)
	Load(t, context.Background(), registry, contribute(values...))
	return registry
}

// Tools returns a loaded executor holding the given tools.
func Tools(t testing.TB, values ...tool.Tool) tool.Executor {
	t.Helper()
	return ToolsWithHooks(t, nil, values...)
}

// ToolsWithHooks is Tools with the tool registry bound to a hook registry.
func ToolsWithHooks(t testing.TB, registry *hooks.Registry, values ...tool.Tool) tool.Executor {
	t.Helper()
	if len(values) == 0 {
		return tool.EmptyExecutor()
	}
	tools := toolset.NewRegistry(registry)
	Load(t, context.Background(), tools, contribute(values...))
	return tools
}
