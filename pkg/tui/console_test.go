package tui

import (
	"context"
	"reflect"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/tui/readline/inputrc"
)

func TestAgentConsoleArgsForLineBangCommand(t *testing.T) {
	got, err := AgentConsoleArgsForLine("!echo chat_pass")
	if err != nil {
		t.Fatalf("AgentConsoleArgsForLine returned error: %v", err)
	}
	want := []string{"!", "echo chat_pass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentConsoleArgsForLine = %#v, want %#v", got, want)
	}
}

func TestAgentReadlineBackspaceBindings(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert"} {
		for _, seq := range []string{inputrc.Unescape(`\C-h`), inputrc.Unescape(`\C-?`)} {
			bind, ok := shell.Config.Binds[keymap][seq]
			if !ok {
				t.Fatalf("%s missing bind for %q", keymap, inputrc.Escape(seq))
			}
			if bind.Action != "backward-delete-char" {
				t.Fatalf("%s %q action = %q", keymap, inputrc.Escape(seq), bind.Action)
			}
		}
	}
}

func TestAgentReadlinePendingBracketedPaste(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	if !shell.HandleBracketedPastePending("[200~demo_reqresp\x1b[201~") {
		t.Fatal("pending bracketed paste was not handled")
	}
	if got := string(*shell.Line()); got != "demo_reqresp" {
		t.Fatalf("single-line paste = %q", got)
	}
}

func TestAgentReadlinePendingMultilinePasteReference(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	if !shell.HandleBracketedPastePending("[200~alpha\nbeta\x1b[201~") {
		t.Fatal("pending bracketed paste was not handled")
	}
	const placeholder = "[Pasted text #1 +2 lines]"
	if got := string(*shell.Line()); got != placeholder {
		t.Fatalf("multiline paste = %q", got)
	}
	_, resolved := repl.resolvePastedText(placeholder)
	if resolved != "alpha\nbeta" {
		t.Fatalf("resolved paste = %q", resolved)
	}
}
