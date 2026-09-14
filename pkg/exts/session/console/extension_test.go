package console_test

import (
	"context"
	"github.com/chainreactors/aiscan/core/commandline"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/console/api"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	contributor "github.com/chainreactors/aiscan/pkg/exts/session/console"
	tuiext "github.com/chainreactors/aiscan/pkg/exts/tui"
	"github.com/chainreactors/aiscan/pkg/types"
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func TestProviderDependencyAndPerTerminalSessionDispatch(t *testing.T) {
	tui := tuiext.New()
	session, err := sessionext.New(sessionext.Config{Commands: []sessionext.Command{{
		Spec:    &types.CommandSpec{Name: "/inspect", Aliases: []string{"/i"}},
		Handler: func(context.Context, *sessionext.Session, []string) (*types.CommandResult, error) { return nil, nil },
	}}})
	if err != nil {
		t.Fatal(err)
	}
	presentation, err := contributor.New(tui.Registrar(), session.Runtime())
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately reverse declaration order; the graph supplies ordering.
	set, err := extension.New(
		extension.Entry{ID: "session.repl", DependsOn: []string{"tui", "session"}, Extension: presentation},
		extension.Entry{ID: "session", Extension: session},
		extension.Entry{ID: "tui", Extension: tui},
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
		args, err := commandline.SplitCommandLine(received)
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
	tui := tuiext.New() // Not included in this graph.
	session, err := sessionext.New(sessionext.Config{})
	if err != nil {
		t.Fatal(err)
	}
	presentation, err := contributor.New(tui.Registrar(), session.Runtime())
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "session.repl", Extension: presentation})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if err := set.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "not accepting") {
		t.Fatalf("missing TUI provider: %v", err)
	}
	if set.Active() {
		t.Fatal("failed profile published")
	}
}
