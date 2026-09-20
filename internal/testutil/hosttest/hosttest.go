// Package hosttest builds extension hosts owned by a single test. It exists so
// that package tests do not each re-implement the same three-line host wiring.
//
// Production code must construct an explicit Profile instead. The guard test in
// this package fails if any non-test package links it.
package hosttest

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
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
func Commands(t testing.TB, values ...coretool.Command) *coretool.CommandRegistry {
	t.Helper()
	registry := coretool.NewCommandRegistry()
	Load(t, context.Background(), extension.Provided[*hooks.Registry](hooks.New()), registry, contribute(values...))
	return registry
}

// Tools returns a loaded executor holding the given tools.
func Tools(t testing.TB, values ...coretool.Tool) coretool.Executor {
	t.Helper()
	return ToolsWithHooks(t, nil, values...)
}

// ToolsWithHooks is Tools with the tool registry bound to a hook registry.
func ToolsWithHooks(t testing.TB, registry *hooks.Registry, values ...coretool.Tool) coretool.Executor {
	t.Helper()
	if len(values) == 0 {
		return coretool.EmptyExecutor()
	}
	if registry == nil {
		registry = hooks.New()
	}
	tools := coretool.NewToolRegistry()
	Load(t, context.Background(), extension.Provided[*hooks.Registry](registry), tools, contribute(values...))
	return tools
}

// Capabilities is the set every extension can expect from a profile: a hook
// registry, an event stream, and a routing endpoint that routes nothing.
func Capabilities() extension.Extension {
	registry, stream := hooks.New(), events.New()
	return extension.Func{LoadFunc: func(scope *extension.Scope) error {
		if err := extension.Provide[*hooks.Registry](scope, registry); err != nil {
			return err
		}
		if err := extension.Provide[*events.Stream](scope, stream); err != nil {
			return err
		}
		return extension.Provide[egress.Endpoint](scope, egress.Disabled())
	}}
}
