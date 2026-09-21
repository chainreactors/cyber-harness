package terminal

import (
	"context"
	"github.com/chainreactors/cyber/core/hooks"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type testCommandBatch struct {
	commands []coretool.Command
}

func commandBatch(cmds ...coretool.Command) testCommandBatch {
	return testCommandBatch{commands: cmds}
}

func loadTestRegistry(t *testing.T, batches ...testCommandBatch) (*coretool.CommandRegistry, *extension.Set) {
	t.Helper()
	registry := coretool.NewCommandRegistry()
	entries := []extension.Extension{extension.Provided[*hooks.Registry](hooks.New()), registry}
	for _, batch := range batches {
		batch := batch
		entries = append(entries,
			extension.Func{LoadFunc: func(scope *extension.Scope) error {
				return extension.Add(scope, batch.commands...)
			}},
		)
	}
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
