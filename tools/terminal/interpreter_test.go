package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/utils/proc"
)

type adapterTestExitError struct{ code int }

func (e adapterTestExitError) Error() string { return fmt.Sprintf("exit %d", e.code) }
func (e adapterTestExitError) ExitCode() int { return e.code }

type adapterTestCommands struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newAdapterTestBash(t *testing.T) (*BashTool, *coretool.CommandRegistry, *adapterTestCommands) {
	t.Helper()
	state := &adapterTestCommands{started: make(chan struct{}), canceled: make(chan struct{})}
	registry, _ := loadTestRegistry(t, commandBatch(
		coretool.Command{Name: "memory_echo", Run: func(_ context.Context, execution *coretool.Execution) (any, error) {
			fmt.Fprintln(execution.Stdout, strings.Join(execution.Args, " "))
			return nil, nil
		}},
		coretool.Command{Name: "memory_upper", Run: func(_ context.Context, execution *coretool.Execution) (any, error) {
			data, err := io.ReadAll(execution.Stdin)
			if err != nil {
				return nil, err
			}
			_, err = execution.Stdout.Write(bytes.ToUpper(data))
			return nil, err
		}},
		coretool.Command{Name: "memory_fail", Run: func(context.Context, *coretool.Execution) (any, error) {
			return nil, adapterTestExitError{code: 7}
		}},
		coretool.Command{Name: "memory_context", Run: func(ctx context.Context, execution *coretool.Execution) (any, error) {
			invocation := operation.InvocationFromContext(ctx)
			fmt.Fprintf(execution.Stdout, "dir=%s call=%s session=%s turn=%s emitter=%s\n",
				execution.Dir, invocation.CallID, invocation.SessionID, invocation.TurnID, invocation.Emitter)
			return nil, nil
		}},
		coretool.Command{Name: "memory_wait", Run: func(ctx context.Context, _ *coretool.Execution) (any, error) {
			state.once.Do(func() { close(state.started) })
			<-ctx.Done()
			close(state.canceled)
			return nil, ctx.Err()
		}},
		coretool.Command{Name: "scan", Run: func(_ context.Context, execution *coretool.Execution) (any, error) {
			fmt.Fprintln(execution.Stdout, strings.Join(execution.Args, " "))
			return nil, nil
		}},
	))
	bash := NewBashTool(t.TempDir(), 10, nil)
	bash.SetCommandRegistry(registry)
	t.Cleanup(bash.Close)
	return bash, registry, state
}

func runAdapterCommand(t *testing.T, bash *BashTool, ctx context.Context, command string, workDir string) (proc.Info, string) {
	t.Helper()
	var output strings.Builder
	execution, err := bash.RunForeground(ctx, command, BashExecOptions{
		WorkDir:  workDir,
		OnOutput: func(data []byte) { _, _ = output.Write(data) },
	})
	if err != nil {
		t.Fatalf("RunForeground(%q): %v", command, err)
	}
	info, ok := execution.Session()
	if !ok {
		t.Fatalf("RunForeground(%q) has no retained session", command)
	}
	return info, output.String()
}

func TestShellCommandComposition(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	dir := t.TempDir()
	for _, test := range []struct {
		command, output string
		status          int
	}{
		{"memory_echo one && memory_echo two", "one\ntwo", 0},
		{"memory_fail || memory_echo recovered", "recovered", 0},
		{"memory_echo hello | memory_upper", "HELLO", 0},
		{"memory_fail && memory_echo unreachable", "", 7},
	} {
		info, output := runAdapterCommand(t, bash, t.Context(), test.command, dir)
		if info.ExitStatus() != test.status || strings.TrimSpace(output) != test.output {
			t.Fatalf("%q: exit=%d output=%q", test.command, info.ExitStatus(), output)
		}
	}
}

func TestInterpreterPreservesNativeCommandValues(t *testing.T) {
	want := []string{"", "two words", "|", ">", "&&", "$HOME", `a\nb`}
	details := func() int { return 42 }
	registry, _ := loadTestRegistry(t, commandBatch(
		coretool.Command{Name: "native-args", Run: func(_ context.Context, e *coretool.Execution) (any, error) {
			return details, json.NewEncoder(e.Stdout).Encode(e.Args)
		}},
		coretool.Command{Name: "native-bytes", Run: func(_ context.Context, e *coretool.Execution) (any, error) {
			_, err := e.Stdout.Write([]byte{0, 255, 10, 13, 65})
			return nil, err
		}},
		coretool.Command{Name: "native-copy", Run: func(_ context.Context, e *coretool.Execution) (any, error) {
			_, err := io.Copy(e.Stdout, e.Stdin)
			return nil, err
		}},
		coretool.Command{Name: "native-env", Run: func(_ context.Context, e *coretool.Execution) (any, error) {
			if !slices.Contains(e.Env, "NATIVE_LOCAL=two words") {
				return nil, fmt.Errorf("shell assignment missing from command environment")
			}
			return nil, nil
		}},
	))
	bash := NewBashTool(t.TempDir(), 5, nil)
	bash.SetCommandRegistry(registry)
	t.Cleanup(bash.Close)
	var out bytes.Buffer
	execution, err := bash.RunForeground(t.Context(), `native-args '' 'two words' '|' '>' '&&' '$HOME' 'a\nb'`, BashExecOptions{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !slices.Equal(got, want) {
		t.Fatalf("argv=%q, want %q, decode error=%v", got, want, err)
	}
	if f, ok := execution.Details.(func() int); !ok || f() != 42 {
		t.Fatalf("command details lost: %T", execution.Details)
	}
	result, err := bash.RunForegroundTool(t.Context(), "native-bytes | native-copy > bytes.bin; NATIVE_LOCAL='two words' native-env", BashExecOptions{})
	if err != nil || result.IsError {
		t.Fatalf("composed native commands: result=%v err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(bash.workDir, "bytes.bin"))
	if err != nil || !bytes.Equal(data, []byte{0, 255, 10, 13, 65}) {
		t.Fatalf("binary redirect=%v err=%v", data, err)
	}
}

func TestShellCommandRedirectionAndInvocationContext(t *testing.T) {
	bash, _, _ := newAdapterTestBash(t)
	dir := t.TempDir()
	ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{
		WorkDir: dir, CallID: "call-1", SessionID: "session-1", TurnID: "turn-1", Emitter: "runner",
	})
	info, _ := runAdapterCommand(t, bash, ctx, "memory_context > context.txt", dir)
	data, err := os.ReadFile(filepath.Join(dir, "context.txt"))
	if err != nil || info.ExitStatus() != 0 {
		t.Fatalf("redirection: status=%d err=%v", info.ExitStatus(), err)
	}
	for _, want := range []string{"dir=" + dir, "call=call-1", "session=session-1", "turn=turn-1", "emitter=runner"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("redirected context missing %q: %q", want, data)
		}
	}
}

func TestInterpreterCloseCancelsCommand(t *testing.T) {
	bash, _, state := newAdapterTestBash(t)
	execution, err := bash.Start(t.Context(), "memory_wait && memory_echo unreachable", BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-state.started:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not start")
	}
	bash.Close()
	select {
	case <-state.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not stop")
	}
	<-bash.tasks.Done(execution.ID)
}
