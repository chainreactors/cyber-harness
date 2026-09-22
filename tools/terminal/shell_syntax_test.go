package terminal

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestShellSyntaxBeforeArgumentSplitting(t *testing.T) {
	for _, line := range []string{
		`gogo -P port; echo "exit=$?"`, `gogo --help&&echo done`,
		"gogo --help\necho done", `gogo --help>out`, `gogo --help|cat`,
		`memory_echo "$PATH"`, "memory_echo `date`",
	} {
		if shellSyntaxIndex(line) < 0 {
			t.Errorf("missed shell syntax: %s", line)
		}
	}
	for _, line := range []string{`gogo -P port`, `memory_echo 'a;b&&c'`, `memory_echo "a;b"`, `memory_echo '&&'`} {
		if shellSyntaxIndex(line) >= 0 {
			t.Errorf("literal routed through shell: %s", line)
		}
	}
}

func TestFirstCommandTokenWithShellSyntax(t *testing.T) {
	for _, line := range []string{`gogo;echo done`, `gogo&&echo done`, `gogo "$PATH"`, `gogo --help>out`} {
		if got := firstCommandToken(line); got != "gogo" {
			t.Errorf("command %q: %q", line, got)
		}
	}
}

func TestAdjacentShellOperatorsExecuteBothCommands(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	commands := []string{"memory_echo one&&memory_echo two"}
	if runtime.GOOS != "windows" {
		commands = append(commands, "memory_echo one;memory_echo two", "memory_echo one\nmemory_echo two")
	}
	for _, command := range commands {
		session, output := runAdapterCommand(t, bash, context.Background(), command, t.TempDir())
		if session.ExitStatus() != 0 || !strings.Contains(output, "one") || !strings.Contains(output, "two") || strings.Contains(output, "&&") {
			t.Fatalf("composition %q: exit=%d output=%q", command, session.ExitStatus(), output)
		}
	}
}

func TestQuotedShellOperatorsRemainLiteralArguments(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	session, output := runAdapterCommand(t, bash, context.Background(), `memory_echo 'a;b' '&&'`, t.TempDir())
	if session.ExitStatus() != 0 || !strings.Contains(output, "a;b &&") || bash.shellAdapter != nil {
		t.Fatalf("literal arguments: exit=%d output=%q adapter=%v", session.ExitStatus(), output, bash.shellAdapter)
	}
}
