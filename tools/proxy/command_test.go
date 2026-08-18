package proxy

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
)

func runProxy(cmd *Command, args ...string) (string, error) {
	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &commands.Execution{Args: args, Stdout: &output, Stderr: &output})
	return output.String(), err
}

func TestCommandName(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	if cmd.Name() != "proxy" {
		t.Fatalf("Name() = %q, want proxy", cmd.Name())
	}
}

func TestUsageNotEmpty(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	usage := cmd.Usage()
	if !strings.Contains(usage, "proxy") {
		t.Fatalf("Usage() missing 'proxy': %s", usage)
	}
}

func TestNoArgsReturnsUsage(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	out, err := runProxy(cmd)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "proxy") {
		t.Fatalf("expected usage, got: %q", out)
	}
}

func TestCurrentNoProxy(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	out, err := runProxy(cmd, "current")
	if err != nil {
		t.Fatalf("current error = %v", err)
	}
	if !strings.Contains(out, "no proxy") {
		t.Fatalf("expected 'no proxy', got: %q", out)
	}
}

func TestCurrentWithOriginalProxy(t *testing.T) {
	state := NewState("socks5://127.0.0.1:1080")
	cmd := New(state)
	out, err := runProxy(cmd, "current")
	if err != nil {
		t.Fatalf("current error = %v", err)
	}
	if !strings.Contains(out, "socks5://127.0.0.1:1080") {
		t.Fatalf("expected original proxy in output, got: %q", out)
	}
}

func TestListNoSubscription(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	out, err := runProxy(cmd, "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !strings.Contains(out, "no subscription") {
		t.Fatalf("expected 'no subscription', got: %q", out)
	}
}

func TestSwitchNoSubscription(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "switch", "node1")
	if err == nil {
		t.Fatal("expected error for switch without subscription")
	}
	if !strings.Contains(err.Error(), "no subscription") {
		t.Fatalf("error = %v, want 'no subscription'", err)
	}
}

func TestClear(t *testing.T) {
	state := NewState("socks5://127.0.0.1:1080")
	cmd := New(state)

	out, err := runProxy(cmd, "clear")
	if err != nil {
		t.Fatalf("clear error = %v", err)
	}
	if !strings.Contains(out, "cleared") {
		t.Fatalf("expected 'cleared', got: %q", out)
	}
	// Clear reverts the egress to the original proxy; the message reports it and
	// the republished chain must remain usable.
	if !strings.Contains(out, "socks5://127.0.0.1:1080") {
		t.Fatalf("expected revert to original proxy in output, got: %q", out)
	}
	if state.CurrentDial() == nil {
		t.Fatal("CurrentDial must remain non-nil after clear")
	}
}

func TestPassthroughMissingCommand(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "socks5://127.0.0.1:1080")
	if err == nil {
		t.Fatal("expected error for passthrough without command")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Fatalf("error = %v, want usage hint", err)
	}
}

func TestPassthroughNoExecutor(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "socks5://127.0.0.1:1080", "gogo", "-i", "10.0.0.1")
	if err == nil {
		t.Fatal("expected error when no executor set")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("error = %v, want 'not available'", err)
	}
}

func TestPassthroughSetsAndRevertsProxy(t *testing.T) {
	state := NewState("socks5://127.0.0.1:1080")
	cmd := New(state)
	base := state.dialPtr()

	var duringExec = base
	cmd.SetCommandExecutor(func(_ context.Context, tokens []string, execution *commands.Execution) (any, error) {
		duringExec = state.dialPtr()
		fmt.Fprint(execution.Stdout, "executed: "+strings.Join(tokens, " "))
		return nil, nil
	})

	out, err := runProxy(cmd, "socks5://127.0.0.1:9999", "echo", "hello")
	if err != nil {
		t.Fatalf("passthrough error = %v", err)
	}
	if !strings.Contains(out, "executed: echo hello") {
		t.Fatalf("expected command output, got: %q", out)
	}
	// The override republishes a different chain for the duration of the wrapped
	// command, then restores the previous one.
	if duringExec == base {
		t.Fatal("expected egress chain to be overridden during passthrough execution")
	}
	if state.dialPtr() != base {
		t.Fatal("expected egress chain to be restored after passthrough")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "invalid")
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown proxy subcommand") {
		t.Fatalf("error = %v, want 'unknown proxy subcommand'", err)
	}
}

func TestSubscribeMissingURL(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "subscribe")
	if err == nil {
		t.Fatal("expected error for subscribe without URL")
	}
}

func TestAutoMissingURL(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "auto")
	if err == nil {
		t.Fatal("expected error for auto without URL")
	}
}

func TestTestNoSubscription(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "test")
	if err == nil {
		t.Fatal("expected error for test without subscription")
	}
	if !strings.Contains(err.Error(), "no subscription") {
		t.Fatalf("error = %v, want 'no subscription'", err)
	}
}

func TestSwitchMissingArg(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := runProxy(cmd, "switch")
	if err == nil {
		t.Fatal("expected error for switch without arg")
	}
}
