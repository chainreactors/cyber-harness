package command

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
)

func tmuxTool(t *testing.T) *TmuxCommand {
	t.Helper()
	mgr := tmuxpkg.NewManager()
	t.Cleanup(mgr.Shutdown)
	return &TmuxCommand{
		manager: mgr,
		workDir: t.TempDir(),
	}
}

func TestTmuxNewSessionForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)
	out, err := tmux.Execute(context.Background(), []string{"new-session", "echo", "hello"})
	if err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected 'hello' in output, got: %q", out)
	}
}

func TestTmuxNewSessionDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)
	out, err := tmux.Execute(context.Background(), []string{"new-session", "-d", "sleep", "10"})
	if err != nil {
		t.Fatalf("new-session -d: %v", err)
	}
	if !strings.Contains(out, "[detached]") {
		t.Fatalf("expected '[detached]' in output, got: %q", out)
	}

	// Session should appear in list
	ls, err := tmux.Execute(context.Background(), []string{"ls"})
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if strings.Contains(ls, "no server") {
		t.Fatalf("expected sessions, got: %q", ls)
	}
	if !strings.Contains(ls, "running") {
		t.Fatalf("expected running session, got: %q", ls)
	}
}

func TestTmuxNewSessionWithName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)
	_, err := tmux.Execute(context.Background(), []string{"new", "-d", "-s", "myscan", "sleep", "5"})
	if err != nil {
		t.Fatalf("new -s: %v", err)
	}

	ls, _ := tmux.Execute(context.Background(), []string{"ls"})
	if !strings.Contains(ls, "myscan") {
		t.Fatalf("expected session name 'myscan' in ls, got: %q", ls)
	}
}

func TestTmuxListSessionsEmpty(t *testing.T) {
	tmux := tmuxTool(t)
	out, err := tmux.Execute(context.Background(), []string{"list-sessions"})
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	if !strings.Contains(out, "no server") {
		t.Fatalf("expected 'no server' for empty list, got: %q", out)
	}
}

func TestTmuxSendKeys(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	// Start head -1 which reads one line
	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "input", "head", "-1"})

	time.Sleep(200 * time.Millisecond)

	// Send text + Enter
	out, err := tmux.Execute(context.Background(), []string{"send-keys", "-t", "input", "hello world", "Enter"})
	if err != nil {
		t.Fatalf("send-keys: %v", err)
	}
	if !strings.Contains(out, "sent") {
		t.Fatalf("expected 'sent' in output, got: %q", out)
	}

	// Wait for completion
	sessions := tmux.manager.List()
	if len(sessions) == 0 {
		t.Fatal("no sessions")
	}
	<-tmux.manager.Done(sessions[0].ID)

	// Check output contains our input
	peek, _ := tmux.Execute(context.Background(), []string{"capture-pane", "-t", sessions[0].ID})
	if !strings.Contains(peek, "hello world") {
		t.Fatalf("expected 'hello world' in capture, got: %q", peek)
	}
}

func TestTmuxSendKeysSpecialKeys(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	// head -2 reads exactly 2 lines then exits
	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "keys", "head", "-2"})
	time.Sleep(200 * time.Millisecond)

	// Send two lines with Enter special key
	tmux.Execute(context.Background(), []string{"send-keys", "-t", "keys", "line1", "Enter"})
	time.Sleep(100 * time.Millisecond)
	tmux.Execute(context.Background(), []string{"send-keys", "-t", "keys", "line2", "Enter"})

	<-tmux.manager.Done("keys")

	peek, _ := tmux.Execute(context.Background(), []string{"capture-pane", "-t", "keys"})
	if !strings.Contains(peek, "line1") || !strings.Contains(peek, "line2") {
		t.Fatalf("expected both lines in capture, got: %q", peek)
	}
}

func TestTmuxCapturePaneBasic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "echo", "echo", "captured-output"})
	sessions := tmux.manager.List()
	<-tmux.manager.Done(sessions[0].ID)

	out, err := tmux.Execute(context.Background(), []string{"capture-pane", "-t", sessions[0].ID, "-p"})
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	if !strings.Contains(out, "captured-output") {
		t.Fatalf("expected 'captured-output', got: %q", out)
	}
}

func TestTmuxCapturePaneIncremental(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "inc", "echo part1; sleep 1; echo part2"})

	// Poll until part1 appears in output
	deadline := time.After(3 * time.Second)
	for {
		peek, _ := tmux.Execute(context.Background(), []string{"capture-pane", "-t", "inc"})
		if strings.Contains(peek, "part1") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("part1 never appeared in output")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	// First incremental read — should contain part1
	out1, err := tmux.Execute(context.Background(), []string{"capture-pane", "-t", "inc", "--new"})
	if err != nil {
		t.Fatalf("capture-pane --new first: %v", err)
	}
	if !strings.Contains(out1, "part1") {
		t.Fatalf("first read should contain 'part1', got: %q", out1)
	}

	// Wait for completion
	<-tmux.manager.Done("inc")

	// Second incremental read should have part2 but not part1
	out2, err := tmux.Execute(context.Background(), []string{"capture-pane", "-t", "inc", "--new"})
	if err != nil {
		t.Fatalf("capture-pane --new second: %v", err)
	}
	if strings.Contains(out2, "part1") {
		t.Fatalf("second read should NOT contain 'part1', got: %q", out2)
	}
	if !strings.Contains(out2, "part2") {
		t.Fatalf("second read should contain 'part2', got: %q", out2)
	}

	// Third read should be empty
	out3, _ := tmux.Execute(context.Background(), []string{"capture-pane", "-t", "inc", "--new"})
	if !strings.Contains(out3, "no new output") {
		t.Fatalf("third read should be empty, got: %q", out3)
	}
}

func TestTmuxKillSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "tokill", "sleep", "30"})
	sessions := tmux.manager.List()
	id := sessions[0].ID

	out, err := tmux.Execute(context.Background(), []string{"kill-session", "-t", id})
	if err != nil {
		t.Fatalf("kill-session: %v", err)
	}
	if !strings.Contains(out, "killed") {
		t.Fatalf("expected 'killed' in output, got: %q", out)
	}

	<-tmux.manager.Done(id)
	info, _ := tmux.manager.Get(id)
	if info.State != tmuxpkg.StateKilled {
		t.Fatalf("state = %s, want killed", info.State)
	}
}

func TestTmuxWaitFor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "fast", "echo", "done"})
	sessions := tmux.manager.List()
	id := sessions[0].ID

	out, err := tmux.Execute(context.Background(), []string{"wait-for", "-t", id, "--timeout", "5s"})
	if err != nil {
		t.Fatalf("wait-for: %v", err)
	}
	if !strings.Contains(out, "completed") {
		t.Fatalf("expected 'completed' in wait output, got: %q", out)
	}
}

func TestTmuxWaitForTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	tmux := tmuxTool(t)

	tmux.Execute(context.Background(), []string{"new-session", "-d", "-s", "slow", "sleep", "30"})
	sessions := tmux.manager.List()
	id := sessions[0].ID

	out, err := tmux.Execute(context.Background(), []string{"wait-for", "-t", id, "--timeout", "200ms"})
	if err != nil {
		t.Fatalf("wait-for: %v", err)
	}
	if !strings.Contains(out, "still running") {
		t.Fatalf("expected 'still running' after timeout, got: %q", out)
	}
}

func TestTmuxNoSubcommand(t *testing.T) {
	tmux := tmuxTool(t)
	out, err := tmux.Execute(context.Background(), []string{})
	if err != nil {
		t.Fatalf("expected usage, got error: %v", err)
	}
	if !strings.Contains(out, "new-session") {
		t.Fatalf("expected usage text, got: %q", out)
	}
}

func TestTmuxUnknownSubcommand(t *testing.T) {
	tmux := tmuxTool(t)
	_, err := tmux.Execute(context.Background(), []string{"invalid"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("error = %v, want 'unknown subcommand'", err)
	}
}

func TestTmuxMissingTarget(t *testing.T) {
	tmux := tmuxTool(t)

	for _, cmd := range []string{"send-keys", "capture-pane", "kill-session", "wait-for"} {
		_, err := tmux.Execute(context.Background(), []string{cmd})
		if err == nil {
			t.Fatalf("%s: expected error for missing -t", cmd)
		}
		if !strings.Contains(err.Error(), "-t") {
			t.Fatalf("%s: error = %v, want hint about -t", cmd, err)
		}
	}
}

func TestTmuxNewSessionMissingCommand(t *testing.T) {
	tmux := tmuxTool(t)
	_, err := tmux.Execute(context.Background(), []string{"new-session", "-d"})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
	if !strings.Contains(err.Error(), "missing command") {
		t.Fatalf("error = %v, want 'missing command'", err)
	}
}
