package harness

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func Set(t testing.TB, values ...extension.Extension) *extension.Set {
	t.Helper()
	s, err := extension.New(values...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func Commands(t testing.TB, values ...commands.Command) *commands.Registry {
	t.Helper()
	registry := commands.NewRegistry(nil)
	contribution := extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add(scope, values...) }}
	s := Set(t, registry, contribution)
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return registry
}

func Load(t testing.TB, ctx context.Context, value extension.Extension) *extension.Set {
	t.Helper()
	s := Set(t, value)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func Tools(t testing.TB, values ...tool.Tool) tool.Executor {
	t.Helper()
	return ToolsWithHooks(t, nil, values...)
}

func ToolsWithHooks(t testing.TB, registry *hooks.Registry, values ...tool.Tool) tool.Executor {
	t.Helper()
	if len(values) == 0 {
		return tool.EmptyExecutor()
	}
	toolRegistry := toolset.NewRegistry(registry)
	contribution := extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add(scope, values...) }}
	s := Set(t, toolRegistry, contribution)
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return toolRegistry
}
