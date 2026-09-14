// Package extensiontest constructs owned hosts for tests. Production packages
// must construct their explicit profiles instead.
package extensiontest

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func Set(t testing.TB, entries ...extension.Entry) *extension.Set {
	t.Helper()
	s, err := extension.New(entries...)
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

func Commands(t testing.TB, group string, values ...commands.Command) *commands.Registry {
	return CommandGroups(t, CommandGroup{Name: group, Values: values})
}

type CommandGroup struct {
	Name   string
	Values []commands.Command
}

func CommandGroups(t testing.TB, groups ...CommandGroup) *commands.Registry {
	t.Helper()
	registry := commands.NewRegistry(nil)
	for _, group := range groups {
		if err := registry.Register("fixture", group.Name, group.Values...); err != nil {
			t.Fatal(err)
		}
	}
	s := Set(t, extension.Entry{ID: "command-registry", Extension: registry})
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return registry
}

func Load(t testing.TB, ctx context.Context, value extension.Extension) *extension.Set {
	t.Helper()
	s := Set(t, extension.Entry{ID: "test", Extension: value})
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
	if err := toolRegistry.Register("fixture", values...); err != nil {
		t.Fatal(err)
	}
	s := Set(t,
		extension.Entry{ID: "tool-registry", Extension: toolRegistry},
	)
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return toolRegistry
}
