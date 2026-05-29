package proxy

import (
	"context"
	"strings"
	"testing"
)

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
	out, err := cmd.Execute(context.Background(), nil)
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
	out, err := cmd.Execute(context.Background(), []string{"current"})
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
	out, err := cmd.Execute(context.Background(), []string{"current"})
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
	out, err := cmd.Execute(context.Background(), []string{"list"})
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
	_, err := cmd.Execute(context.Background(), []string{"switch", "node1"})
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
	var lastProxy string
	cmd.SetOnProxyChange(func(p string) { lastProxy = p })

	out, err := cmd.Execute(context.Background(), []string{"clear"})
	if err != nil {
		t.Fatalf("clear error = %v", err)
	}
	if !strings.Contains(out, "cleared") {
		t.Fatalf("expected 'cleared', got: %q", out)
	}
	if lastProxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("expected revert to original proxy, got: %q", lastProxy)
	}
}

func TestPassthroughMissingCommand(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := cmd.Execute(context.Background(), []string{"socks5://127.0.0.1:1080"})
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
	_, err := cmd.Execute(context.Background(), []string{"socks5://127.0.0.1:1080", "gogo", "-i", "10.0.0.1"})
	if err == nil {
		t.Fatal("expected error when no executor set")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("error = %v, want 'not available'", err)
	}
}

func TestPassthroughSetsAndRevertsProxy(t *testing.T) {
	state := NewState("original://proxy")
	cmd := New(state)

	var proxyChanges []string
	cmd.SetOnProxyChange(func(p string) { proxyChanges = append(proxyChanges, p) })
	cmd.SetCommandExecutor(func(_ context.Context, tokens []string) (string, error) {
		return "executed: " + strings.Join(tokens, " "), nil
	})

	out, err := cmd.Execute(context.Background(), []string{"socks5://127.0.0.1:9999", "echo", "hello"})
	if err != nil {
		t.Fatalf("passthrough error = %v", err)
	}
	if !strings.Contains(out, "executed: echo hello") {
		t.Fatalf("expected command output, got: %q", out)
	}
	if len(proxyChanges) != 2 {
		t.Fatalf("expected 2 proxy changes (set + revert), got %d: %v", len(proxyChanges), proxyChanges)
	}
	if proxyChanges[0] != "socks5://127.0.0.1:9999" {
		t.Fatalf("first proxy change = %q, want socks5://127.0.0.1:9999", proxyChanges[0])
	}
	if proxyChanges[1] != "original://proxy" {
		t.Fatalf("second proxy change = %q, want original://proxy (revert)", proxyChanges[1])
	}
}

func TestUnknownSubcommand(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := cmd.Execute(context.Background(), []string{"invalid"})
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
	_, err := cmd.Execute(context.Background(), []string{"subscribe"})
	if err == nil {
		t.Fatal("expected error for subscribe without URL")
	}
}

func TestAutoMissingURL(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := cmd.Execute(context.Background(), []string{"auto"})
	if err == nil {
		t.Fatal("expected error for auto without URL")
	}
}

func TestTestNoSubscription(t *testing.T) {
	state := NewState("")
	cmd := New(state)
	_, err := cmd.Execute(context.Background(), []string{"test"})
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
	_, err := cmd.Execute(context.Background(), []string{"switch"})
	if err == nil {
		t.Fatal("expected error for switch without arg")
	}
}
