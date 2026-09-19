package tmux_test

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tmuxext "github.com/chainreactors/cyber/pkg/exts/tmux"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// tmux borrows the Bash tool rather than being built by whoever owns it, so
// ordering it ahead of the terminal is a load error naming the capability --
// not a nil tool that only fails when someone runs the command.
func TestTmuxContributesItsCommandFromTheBorrowedBashTool(t *testing.T) {
	registry := commands.NewRegistry()
	set, err := extension.New(
		hosttest.Capabilities(),
		registry,
		toolset.NewRegistry(),
		terminalext.New(terminalext.Config{Directory: t.TempDir(), Timeout: 5}),
		tmuxext.New(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !registry.Has("tmux") {
		t.Fatal("tmux extension did not contribute its command")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Has("tmux") {
		t.Fatal("tmux command outlived its extension")
	}
}

func TestTmuxBeforeItsProviderFailsAtLoad(t *testing.T) {
	set, err := extension.New(
		hosttest.Capabilities(),
		commands.NewRegistry(),
		toolset.NewRegistry(),
		tmuxext.New(),
		terminalext.New(terminalext.Config{Directory: t.TempDir(), Timeout: 5}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err == nil {
		t.Fatal("tmux loaded before the extension that owns the Bash tool")
	}
}
