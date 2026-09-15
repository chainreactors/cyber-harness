package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

type testCommandGroup struct {
	id       string
	group    string
	commands []Command
}

func TestRegistryUsesCommandHookBoundaryExactlyOnce(t *testing.T) {
	hookRegistry := hooks.New()
	registry := NewRegistry(hookRegistry)
	runs, decisions, completions := 0, 0, 0
	before := toolhooks.BeforeCommand.On(hookRegistry, "policy", func(_ context.Context, event toolhooks.CommandEvent) (toolhooks.Admission, error) {
		decisions++
		if len(event.Args) > 0 && event.Args[0] == "deny" {
			return toolhooks.Admission{Deny: errors.New("denied by test")}, nil
		}
		return toolhooks.Admission{}, nil
	})
	defer before.Cancel()
	completed := toolhooks.CommandCompleted.On(hookRegistry, "observer", func(_ context.Context, event toolhooks.CommandCompletion) (struct{}, error) {
		completions++
		if event.Command.Name != "echo" || event.Command.Operation == nil {
			t.Errorf("invalid completion: %+v", event)
		}
		return struct{}{}, nil
	})
	defer completed.Cancel()
	command := Command{Name: "echo", Run: func(context.Context, *Execution) (any, error) {
		runs++
		return "ok", nil
	}}
	contributor := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return registry.Register("test", "test", command)
	}}
	set, err := extension.New(
		extension.Entry{ID: "command", Extension: contributor},
		extension.Entry{ID: "registry", DependsOn: []string{"command"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), "echo", &Execution{ID: "call-1", Args: []string{"ok"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), "echo", &Execution{ID: "call-2", Args: []string{"deny"}}); !errors.Is(err, operation.ErrDenied) {
		t.Fatalf("denied command: %v", err)
	}
	if runs != 1 || decisions != 2 || completions != 2 {
		t.Fatalf("runs=%d before=%d completed=%d", runs, decisions, completions)
	}
}

func commandGroup(id, group string, commands ...Command) testCommandGroup {
	return testCommandGroup{id: id, group: group, commands: commands}
}

func loadTestRegistry(t *testing.T, groups ...testCommandGroup) (*Registry, *extension.Set) {
	t.Helper()
	registry := NewRegistry(nil)
	entries := make([]extension.Entry, 0, len(groups)+1)
	dependencies := make([]string, 0, len(groups))
	for _, group := range groups {
		group := group
		dependencies = append(dependencies, group.id)
		entries = append(entries, extension.Entry{
			ID: group.id,
			Extension: extension.Func{LoadFunc: func(scope *extension.Scope) error {
				return registry.Register("test", group.group, group.commands...)
			}},
		})
	}
	entries = append(entries, extension.Entry{ID: "registry", DependsOn: dependencies, Extension: registry})
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		_ = set.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Errorf("close command registry: %v", err)
		}
	})
	return registry, set
}
