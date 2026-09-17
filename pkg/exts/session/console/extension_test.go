package console_test

import (
	"context"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/console/api"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	contributor "github.com/chainreactors/cyber/pkg/exts/session/console"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func TestProviderDependencyAndPerTerminalSessionDispatch(t *testing.T) {
	tui := tuiext.New()
	session, err := sessionext.New(agentsession.Config{Commands: []agentsession.Command{{
		Spec:    &types.CommandSpec{Name: "/inspect", Aliases: []string{"/i"}},
		Handler: func(context.Context, *agentsession.Session, []string) (*types.CommandResult, error) { return nil, nil },
	}}})
	if err != nil {
		t.Fatal(err)
	}
	presentation, err := contributor.New(session.Runtime())
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(
		tui,
		session,
		presentation,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	bindings := tui.Bindings()
	for _, terminal := range []string{"first", "second"} {
		var received string
		root := &cobra.Command{Use: terminal}
		root.AddCommand(bindings.Commands(api.View{Command: func(line string) error { received = line; return nil }})...)
		root.SetArgs([]string{"/i", "two words", "--literal"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		args, err := commands.SplitCommandLine(received)
		if err != nil || len(args) != 3 || args[0] != "/inspect" || args[1] != "two words" || args[2] != "--literal" {
			t.Fatalf("%s: %q %v", terminal, received, err)
		}
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if tui.Bindings() != nil {
		t.Fatal("closed TUI published bindings")
	}
}

func TestMissingProviderLoadFailsContribution(t *testing.T) {
	session, err := sessionext.New(agentsession.Config{})
	if err != nil {
		t.Fatal(err)
	}
	presentation, err := contributor.New(session.Runtime())
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(presentation)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if err := set.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "resource type is not defined") {
		t.Fatalf("missing TUI provider: %v", err)
	}
	if set.Active() {
		t.Fatal("failed profile published")
	}
}
