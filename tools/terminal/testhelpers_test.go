package terminal

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
)

type testCommandBatch struct {
	commands []commands.Command
}

func commandBatch(cmds ...commands.Command) testCommandBatch {
	return testCommandBatch{commands: cmds}
}

func loadTestRegistry(t *testing.T, batches ...testCommandBatch) (*commands.Registry, *extension.Set) {
	t.Helper()
	registry := commands.NewRegistry(nil)
	entries := []extension.Extension{registry}
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
