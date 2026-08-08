package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coretool "github.com/chainreactors/aiscan/core/tool"
)

type adapterTestExitError struct{ code int }

func (e adapterTestExitError) Error() string { return fmt.Sprintf("adapter test exit %d", e.code) }
func (e adapterTestExitError) ExitCode() int { return e.code }

type adapterTestCommands struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newAdapterTestBash(t *testing.T) (*BashTool, *CommandRegistry, *adapterTestCommands) {
	t.Helper()
	state := &adapterTestCommands{started: make(chan struct{}), canceled: make(chan struct{})}
	registry := NewRegistry()
	registry.Register(Command{Name: "memory_echo", Run: func(_ context.Context, execution *Execution) (any, error) {
		fmt.Fprintln(execution.Stdout, strings.Join(execution.Args, " "))
		return nil, nil
	}}, "test")
	registry.Register(Command{Name: "memory_upper", Run: func(_ context.Context, execution *Execution) (any, error) {
		data, err := io.ReadAll(execution.Stdin)
		if err != nil {
			return nil, err
		}
		_, err = execution.Stdout.Write(bytes.ToUpper(data))
		return nil, err
	}}, "test")
	registry.Register(Command{Name: "memory_fail", Run: func(context.Context, *Execution) (any, error) {
		return nil, adapterTestExitError{code: 7}
	}}, "test")
	registry.Register(Command{Name: "memory_context", Run: func(ctx context.Context, execution *Execution) (any, error) {
		invocation := coretool.InvocationFromContext(ctx)
		fmt.Fprintf(execution.Stdout, "dir=%s call=%s session=%s turn=%s emitter=%s\n",
			execution.Dir, invocation.CallID, invocation.SessionID, invocation.TurnID, invocation.Emitter)
		return nil, nil
	}}, "test")
	registry.Register(Command{Name: "memory_wait", Run: func(ctx context.Context, _ *Execution) (any, error) {
		state.once.Do(func() { close(state.started) })
		<-ctx.Done()
		close(state.canceled)
		return nil, ctx.Err()
	}}, "test")

	bash := NewBashTool(t.TempDir(), 10)
	bash.SetCommandNames(registry.Names)
	bash.SetCommandResolver(registry.Get)
	bash.attachShellCommands(registry)
	t.Cleanup(bash.Close)
	return bash, registry, state
}

func runAdapterCommand(t *testing.T, bash *BashTool, ctx context.Context, command string, workDir string) (*Execution, string) {
	t.Helper()
	var output strings.Builder
	execution, err := bash.RunForeground(ctx, command, BashExecOptions{
		WorkDir: workDir,
		OnOutput: func(data []byte) {
			_, _ = output.Write(data)
		},
	})
	if err != nil {
		t.Fatalf("RunForeground(%q): %v", command, err)
	}
	return execution, output.String()
}

func TestShellCommandAdapterIsLazy(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	execution, output := runAdapterCommand(t, bash, context.Background(), "memory_echo direct", t.TempDir())
	if execution.ExitCode != 0 || !strings.Contains(output, "direct") {
		t.Fatalf("direct command exit=%d output=%q", execution.ExitCode, output)
	}
	if bash.shellAdapter != nil {
		t.Fatal("simple registered command allocated shell runtime state")
	}
}

func TestShellCommandMarkerDoesNotHijackNormalChildProcess(t *testing.T) {
	t.Setenv(shellCommandAdapterMarkerEnv, "1")
	t.Setenv(shellCommandAdapterCommandEnv, "")
	if code, ok := runShellCommandProxyIfRequested(); ok {
		t.Fatalf("marker-only child was treated as proxy with code %d", code)
	}
}

func TestShellCommandComposition(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	workDir := t.TempDir()

	execution, output := runAdapterCommand(t, bash, context.Background(), "memory_echo one && memory_echo two", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "one") || !strings.Contains(output, "two") {
		t.Fatalf("and composition exit=%d output=%q", execution.ExitCode, output)
	}

	execution, output = runAdapterCommand(t, bash, context.Background(), "memory_fail || memory_echo recovered", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "recovered") {
		t.Fatalf("or composition exit=%d output=%q", execution.ExitCode, output)
	}

	execution, output = runAdapterCommand(t, bash, context.Background(), "memory_echo hello | memory_upper", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "HELLO") {
		t.Fatalf("pipeline exit=%d output=%q", execution.ExitCode, output)
	}

	execution, _ = runAdapterCommand(t, bash, context.Background(), "memory_fail && memory_echo unreachable", workDir)
	if execution.ExitCode != 7 {
		t.Fatalf("short-circuit exit code = %d, want 7", execution.ExitCode)
	}
}

func TestShellCommandRedirectionAndInvocationContext(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	workDir := t.TempDir()
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{
		WorkDir: workDir, CallID: "call-1", SessionID: "session-1", TurnID: "turn-1", Emitter: "runner",
	})

	execution, _ := runAdapterCommand(t, bash, ctx, "memory_context > adapter-context.txt", workDir)
	if execution.ExitCode != 0 {
		t.Fatalf("redirection exit code = %d", execution.ExitCode)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "adapter-context.txt"))
	if err != nil {
		t.Fatalf("read redirected output: %v", err)
	}
	text := string(data)
	for _, expected := range []string{"dir=" + workDir, "call=call-1", "session=session-1", "turn=turn-1", "emitter=runner"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("redirected context missing %q: %q", expected, text)
		}
	}
}

func TestShellCommandAdapterFindsCommandsRegisteredLater(t *testing.T) {
	bash, registry, _ := newAdapterTestBash(t)
	registry.Register(Command{Name: "scan", Run: func(_ context.Context, execution *Execution) (any, error) {
		fmt.Fprintln(execution.Stdout, strings.Join(execution.Args, " "))
		return nil, nil
	}}, "test")
	command := "scan -i http://127.0.0.1:1 --timeout 1 --no-color && memory_echo ready"
	execution, output := runAdapterCommand(t, bash, context.Background(), command, t.TempDir())
	if execution.ExitCode != 0 || !strings.Contains(output, "http://127.0.0.1:1") || !strings.Contains(output, "ready") {
		t.Fatalf("late alias exit=%d output=%q", execution.ExitCode, output)
	}
}

func TestShellCommandCloseCancelsCallsAndRemovesRuntime(t *testing.T) {
	bash, _, state := newAdapterTestBash(t)
	execution, err := bash.Start(context.Background(), "memory_wait && memory_echo unreachable", BashExecOptions{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start memory_wait: %v", err)
	}
	runtimeDir := bash.shellAdapter.runtimeDir
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("runtime directory before close: %v", err)
	}
	select {
	case <-state.started:
	case <-time.After(5 * time.Second):
		t.Fatal("shell command did not start")
	}
	bash.Close()
	bash.Close()
	select {
	case <-state.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("shell command was not canceled")
	}
	if err := execution.Wait(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("wait after close: %v", err)
	}
	if _, err := os.Stat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime directory still exists after close: %v", err)
	}
}

func TestShellCommandDisconnectCancelsRunningCommand(t *testing.T) {
	bash, _, state := newAdapterTestBash(t)
	adapter, err := bash.ensureShellCommands()
	if err != nil {
		t.Fatalf("ensure shell commands: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialShellCommandAdapter(ctx, adapter.endpoint)
	if err != nil {
		t.Fatalf("dial shell command adapter: %v", err)
	}
	contextID := adapter.retainContext(context.Background())
	defer adapter.releaseContext(contextID)
	writer := &shellCommandAdapterFrameWriter{writer: conn}
	if err := writer.write(shellCommandAdapterFrame{
		Type: "request", Version: shellCommandAdapterProtocolVersion,
		Command: "memory_wait", Dir: t.TempDir(), ContextID: contextID,
	}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := writer.write(shellCommandAdapterFrame{Type: "stdin_eof"}); err != nil {
		t.Fatalf("write stdin eof: %v", err)
	}
	select {
	case <-state.started:
	case <-ctx.Done():
		t.Fatal("shell command did not start")
	}
	_ = conn.Close()
	select {
	case <-state.canceled:
	case <-ctx.Done():
		t.Fatal("disconnect did not cancel shell command")
	}
}

func TestShellCommandStartupReclaimsOwnedStaleRuntime(t *testing.T) {
	root := shellCommandAdapterRuntimeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := os.MkdirTemp(root, "1073741824-stale-")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleShellCommandAdapterRuntime(); err != nil {
		t.Fatalf("cleanup stale runtime: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale runtime still exists: %v", err)
	}
}
