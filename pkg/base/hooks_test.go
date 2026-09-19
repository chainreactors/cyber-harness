package base_test

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/cyber/pkg/base"
	"github.com/chainreactors/cyber/pkg/commands"
)

// The command registry used to take its hook registry as a constructor
// argument, and the composition root passed nil. hooks is nil-safe, so nothing
// failed -- every admission and observation point simply went quiet, while the
// capability channel published a live registry that files, terminal and proxy
// did pick up. Only the tests that built their own registry stayed honest.
//
// This asserts the two are the same registry, through a real base graph.
func TestCommandsReachTheHookRegistryTheProfilePublishes(t *testing.T) {
	entries, err := base.New(base.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	command := commands.Command{
		Name: "probe",
		Run:  func(context.Context, *commands.Execution) (any, error) { return "ok", nil },
	}
	var registry *hooks.Registry
	var executor commands.Executor
	// Last in the slice: it borrows after everything is published.
	observer := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		if err := extension.Add(scope, command); err != nil {
			return err
		}
		if registry, err = extension.Use[*hooks.Registry](scope); err != nil {
			return err
		}
		executor, err = extension.Use[commands.Executor](scope)
		return err
	}}

	set, err := extension.New(append(entries, observer)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	started, completed := 0, 0
	toolhooks.CommandStarted.On(registry, "probe", func(context.Context, toolhooks.CommandEvent) (struct{}, error) {
		started++
		return struct{}{}, nil
	})
	toolhooks.CommandCompleted.On(registry, "probe", func(context.Context, toolhooks.CommandCompletion) (struct{}, error) {
		completed++
		return struct{}{}, nil
	})

	if _, err := executor.Execute(t.Context(), "probe", &commands.Execution{ID: "probe-1"}); err != nil {
		t.Fatal(err)
	}
	if started != 1 || completed != 1 {
		t.Fatalf("the command registry emits on a different registry than the profile publishes: started=%d completed=%d", started, completed)
	}
}
