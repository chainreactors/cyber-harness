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

func TestMain(m *testing.M) {
	if code, ok := RunCommandBridgeProxyIfRequested(); ok {
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	os.Exit(m.Run())
}

type bridgeTestExitError struct{ code int }

func (e bridgeTestExitError) Error() string { return fmt.Sprintf("bridge test exit %d", e.code) }
func (e bridgeTestExitError) ExitCode() int { return e.code }

type bridgeTestCommands struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newBridgeTestBash(t *testing.T) (*BashTool, *CommandRegistry, *bridgeTestCommands) {
	t.Helper()
	state := &bridgeTestCommands{started: make(chan struct{}), canceled: make(chan struct{})}
	registry := NewRegistry()
	registry.Register(Command{Name: "bridge_echo", Run: func(_ context.Context, execution *Execution) (any, error) {
		fmt.Fprintln(execution.Stdout, strings.Join(execution.Args, " "))
		return nil, nil
	}}, "test")
	registry.Register(Command{Name: "bridge_upper", Run: func(_ context.Context, execution *Execution) (any, error) {
		data, err := io.ReadAll(execution.Stdin)
		if err != nil {
			return nil, err
		}
		_, err = execution.Stdout.Write(bytes.ToUpper(data))
		return nil, err
	}}, "test")
	registry.Register(Command{Name: "bridge_fail", Run: func(context.Context, *Execution) (any, error) {
		return nil, bridgeTestExitError{code: 7}
	}}, "test")
	registry.Register(Command{Name: "bridge_context", Run: func(ctx context.Context, execution *Execution) (any, error) {
		invocation := coretool.InvocationFromContext(ctx)
		fmt.Fprintf(execution.Stdout, "dir=%s call=%s session=%s turn=%s emitter=%s\n",
			execution.Dir, invocation.CallID, invocation.SessionID, invocation.TurnID, invocation.Emitter)
		return nil, nil
	}}, "test")
	registry.Register(Command{Name: "bridge_wait", Run: func(ctx context.Context, _ *Execution) (any, error) {
		state.once.Do(func() { close(state.started) })
		<-ctx.Done()
		close(state.canceled)
		return nil, ctx.Err()
	}}, "test")

	bash := NewBashTool(t.TempDir(), 10)
	bash.SetCommandNames(registry.Names)
	bash.SetCommandResolver(registry.Get)
	if err := bash.EnableCommandBridge(registry); err != nil {
		t.Fatalf("EnableCommandBridge: %v", err)
	}
	t.Cleanup(bash.Close)
	return bash, registry, state
}

func runBridgeCommand(t *testing.T, bash *BashTool, ctx context.Context, command string, workDir string) (*Execution, string) {
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

func TestCommandBridgeDisabledByDefault(t *testing.T) {
	bash := NewBashTool(t.TempDir(), 5)
	defer bash.Close()
	if bash.bridge != nil {
		t.Fatal("disabled bridge allocated runtime state")
	}
}

func TestCommandBridgeMarkerDoesNotHijackNormalChildProcess(t *testing.T) {
	t.Setenv(commandBridgeMarkerEnv, "1")
	t.Setenv(commandBridgeCommandEnv, "")
	if code, ok := RunCommandBridgeProxyIfRequested(); ok {
		t.Fatalf("marker-only child was treated as proxy with code %d", code)
	}
}

func TestCommandBridgeShellComposition(t *testing.T) {
	bash, _, _ := newBridgeTestBash(t)
	workDir := t.TempDir()

	execution, output := runBridgeCommand(t, bash, context.Background(), "bridge_echo one && bridge_echo two", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "one") || !strings.Contains(output, "two") {
		t.Fatalf("and composition exit=%d output=%q", execution.ExitCode, output)
	}

	execution, output = runBridgeCommand(t, bash, context.Background(), "bridge_fail || bridge_echo recovered", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "recovered") {
		t.Fatalf("or composition exit=%d output=%q", execution.ExitCode, output)
	}

	execution, output = runBridgeCommand(t, bash, context.Background(), "bridge_echo hello | bridge_upper", workDir)
	if execution.ExitCode != 0 || !strings.Contains(output, "HELLO") {
		t.Fatalf("pipeline exit=%d output=%q", execution.ExitCode, output)
	}

	execution, _ = runBridgeCommand(t, bash, context.Background(), "bridge_fail && bridge_echo unreachable", workDir)
	if execution.ExitCode != 7 {
		t.Fatalf("short-circuit exit code = %d, want 7", execution.ExitCode)
	}
}

func TestCommandBridgeRedirectionAndInvocationContext(t *testing.T) {
	bash, _, _ := newBridgeTestBash(t)
	workDir := t.TempDir()
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{
		WorkDir: workDir, CallID: "call-1", SessionID: "session-1", TurnID: "turn-1", Emitter: "runner",
	})

	execution, _ := runBridgeCommand(t, bash, ctx, "bridge_context > bridge-context.txt", workDir)
	if execution.ExitCode != 0 {
		t.Fatalf("redirection exit code = %d", execution.ExitCode)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "bridge-context.txt"))
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

func TestCommandBridgeSyncsCommandsRegisteredLater(t *testing.T) {
	bash, registry, _ := newBridgeTestBash(t)
	registry.Register(Command{Name: "bridge_late", Run: func(_ context.Context, execution *Execution) (any, error) {
		fmt.Fprintln(execution.Stdout, "late")
		return nil, nil
	}}, "test")
	if err := bash.SyncCommandBridgeAliases(); err != nil {
		t.Fatalf("SyncCommandBridgeAliases: %v", err)
	}
	execution, output := runBridgeCommand(t, bash, context.Background(), "bridge_late && bridge_echo ready", t.TempDir())
	if execution.ExitCode != 0 || !strings.Contains(output, "late") || !strings.Contains(output, "ready") {
		t.Fatalf("late alias exit=%d output=%q", execution.ExitCode, output)
	}
}

func TestCommandBridgeCloseCancelsCallsAndRemovesRuntime(t *testing.T) {
	bash, _, state := newBridgeTestBash(t)
	runtimeDir := bash.bridge.runtimeDir
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("runtime directory before close: %v", err)
	}

	execution, err := bash.Start(context.Background(), "bridge_wait", BashExecOptions{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start bridge_wait: %v", err)
	}
	select {
	case <-state.started:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge command did not start")
	}
	bash.Close()
	bash.Close()
	select {
	case <-state.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge command was not canceled")
	}
	if err := execution.Wait(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("wait after close: %v", err)
	}
	if _, err := os.Stat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime directory still exists after close: %v", err)
	}
}

func TestCommandBridgeDisconnectCancelsRunningCommand(t *testing.T) {
	bash, _, state := newBridgeTestBash(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialCommandBridge(ctx, bash.bridge.endpoint)
	if err != nil {
		t.Fatalf("dial command bridge: %v", err)
	}
	writer := &commandBridgeFrameWriter{writer: conn}
	if err := writer.write(commandBridgeFrame{
		Type: "request", Version: commandBridgeProtocolVersion,
		Command: "bridge_wait", Dir: t.TempDir(),
	}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := writer.write(commandBridgeFrame{Type: "stdin_eof"}); err != nil {
		t.Fatalf("write stdin eof: %v", err)
	}
	select {
	case <-state.started:
	case <-ctx.Done():
		t.Fatal("bridge command did not start")
	}
	_ = conn.Close()
	select {
	case <-state.canceled:
	case <-ctx.Done():
		t.Fatal("disconnect did not cancel bridge command")
	}
}

func TestCommandBridgeStartupReclaimsOwnedStaleRuntime(t *testing.T) {
	root := commandBridgeRuntimeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := os.MkdirTemp(root, "1073741824-stale-")
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleCommandBridgeRuntime(); err != nil {
		t.Fatalf("cleanup stale runtime: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale runtime still exists: %v", err)
	}
}
