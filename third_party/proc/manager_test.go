package proc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("scenario drives a POSIX shell")
	}
}

// waitState polls until the unit reaches a terminal state, then returns it.
func waitFinished(t *testing.T, m *Manager, id string) Info {
	t.Helper()
	select {
	case <-m.Done(id):
	case <-time.After(30 * time.Second):
		t.Fatalf("unit %s did not finish", id)
	}
	info, ok := m.Get(id)
	if !ok {
		t.Fatalf("unit %s disappeared", id)
	}
	return info
}

func waitUntil(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 1. Every shape reaches a terminal state, and only the OS-backed ones report
// a process. This is the invariant that keeps a meaningless pid off the wire.
func TestShapesShareOneLifecycleAndOnlyProcessesReportAProcess(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	cases := []struct {
		name       string
		attachment Attachment
		shape      Shape
		wantProc   bool
	}{
		{"tty", TTY(ProcOptions{Line: "echo tty-output"}), ShapeTTY, true},
		{"pipe", Pipe(ProcOptions{Binary: "sh", Args: []string{"-c", "echo pipe-output"}}), ShapePipe, true},
		{"func", Func(func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "func-output\n")
			return err
		}), ShapeFunc, false},
		{"extern", externUnit(func(context.Context) error { return nil }), ShapeExtern, false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			info, err := m.Start(t.Context(), Spec{Name: test.name, Command: test.name}, test.attachment)
			if err != nil {
				t.Fatal(err)
			}
			final := waitFinished(t, m, info.ID)
			if final.Shape != test.shape {
				t.Errorf("shape = %q, want %q", final.Shape, test.shape)
			}
			if final.State != StateCompleted {
				t.Errorf("state = %q reason=%q, want completed", final.State, final.Reason)
			}
			if (final.Proc != nil) != test.wantProc {
				t.Errorf("Proc present = %v, want %v", final.Proc != nil, test.wantProc)
			}
			if final.EndedAt.IsZero() {
				t.Error("EndedAt not recorded")
			}
		})
	}
}

// externUnit is the shape a consumer in another module implements: work driven
// elsewhere, with no process, no streams and no exit code. It fills in two
// fields and inherits everything else from the registry.
func externUnit(run func(context.Context) error) Attachment {
	return AttachFunc(func(ctx context.Context) (*Attached, error) {
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			err = run(ctx)
		}()
		return &Attached{
			Shape:  ShapeExtern,
			Wait:   func() Result { <-done; return Result{Err: err} },
			Signal: func(Signal) error { cancel(); return nil },
		}, nil
	})
}

// 2. The ladder escalates when a rung is ignored, and stops climbing when one
// works. A process that traps SIGINT and SIGTERM must still be stoppable.
func TestStopLadderEscalatesPastIgnoredSignals(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	stubborn := `trap '' INT TERM; echo ready; while :; do sleep 0.05; done`
	info, err := m.Start(t.Context(), Spec{Command: "stubborn"}, Pipe(ProcOptions{
		Binary: "sh", Args: []string{"-c", stubborn},
	}))
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		out, _ := m.Peek(info.ID, 10)
		return strings.Contains(out, "ready")
	})

	start := time.Now()
	if err := m.Stop(info.ID, StopOptions{Reason: "test", Grace: 200 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateKilled {
		t.Errorf("state = %q, want killed", final.State)
	}
	if final.Reason != "test" {
		t.Errorf("reason = %q, want test", final.Reason)
	}
	// Two ignored rungs at 200ms each must have elapsed before SIGKILL landed.
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("ladder finished in %s; it cannot have tried interrupt and terminate", elapsed)
	}
}

func TestStopLadderStopsAtTheFirstRungThatWorks(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	polite := `trap 'exit 7' INT; echo ready; while :; do sleep 0.05; done`
	info, err := m.Start(t.Context(), Spec{Command: "polite"}, Pipe(ProcOptions{
		Binary: "sh", Args: []string{"-c", polite},
	}))
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		out, _ := m.Peek(info.ID, 10)
		return strings.Contains(out, "ready")
	})

	start := time.Now()
	if err := m.Stop(info.ID, StopOptions{Reason: "test", Grace: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("interrupt took %s; the ladder did not stop at the first rung", elapsed)
	}
	if final.Proc == nil || final.Proc.ExitCode != 7 {
		t.Errorf("exit = %+v, want the trap's exit 7", final.Proc)
	}
}

// 3. A function that fails has no exit code. The registry must say so rather
// than inventing one, which is what the old implementation did.
func TestFunctionFailureReportsNoExitCode(t *testing.T) {
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "failing"}, Func(func(context.Context, io.Writer) error {
		return errors.New("boom")
	}))
	if err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateFailed {
		t.Errorf("state = %q, want failed", final.State)
	}
	if final.Proc != nil {
		t.Errorf("Proc = %+v, want nil: a function has no exit status", final.Proc)
	}
	if !strings.Contains(final.Reason, "boom") {
		t.Errorf("reason = %q, want the function's error", final.Reason)
	}
}

func TestPanicBecomesTheUnitsFailureNotTheProcesss(t *testing.T) {
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "panicking"}, Func(func(context.Context, io.Writer) error {
		panic("unexpected")
	}))
	if err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateFailed || !strings.Contains(final.Reason, "unexpected") {
		t.Errorf("state=%q reason=%q, want a failure carrying the panic", final.State, final.Reason)
	}
}

// 4. EnvReplace with no entries means an empty environment. EnvInherit cannot
// express that, which is why a caller could never hand a child a clean slate.
func TestEnvReplaceGivesTheChildNothingToInherit(t *testing.T) {
	requireUnix(t)
	t.Setenv("PROC_TEST_LEAK", "leaked")
	m := NewManager()

	read := func(mode EnvMode, env []string) string {
		t.Helper()
		info, err := m.Start(t.Context(), Spec{Command: "env"}, Pipe(ProcOptions{
			Binary: "/bin/sh", Args: []string{"-c", "env"}, Env: env, EnvMode: mode,
		}))
		if err != nil {
			t.Fatal(err)
		}
		waitFinished(t, m, info.ID)
		out, err := m.PeekBytes(info.ID, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if out := read(EnvInherit, nil); !strings.Contains(out, "PROC_TEST_LEAK") {
		t.Error("EnvInherit should pass the current environment through")
	}
	if out := read(EnvReplace, nil); strings.Contains(out, "PROC_TEST_LEAK") {
		t.Errorf("EnvReplace leaked the parent environment:\n%s", out)
	}
	if out := read(EnvReplace, []string{"ONLY=this"}); !strings.Contains(out, "ONLY=this") ||
		strings.Contains(out, "PROC_TEST_LEAK") {
		t.Errorf("EnvReplace should carry exactly what it was given:\n%s", out)
	}
}

// 5 and 6. A protocol stream must never go through the ring buffer, which is
// lossy by construction, and it has exactly one owner.
func TestRawStreamIsByteExactAndClaimedOnce(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	// One line far larger than the ring buffer would keep. It goes through a
	// file because 4 MB does not fit in an argument list.
	payload := strings.Repeat("abcdefghij", 400_000) // 4 MB
	source := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(source, append([]byte(payload), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("cat %q; echo diagnostics >&2", source)

	info, err := m.Start(t.Context(), Spec{Command: "protocol", BufferCap: 64 * 1024}, Pipe(ProcOptions{
		Binary: "/bin/sh", Args: []string{"-c", script}, RawStdout: true,
	}))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := m.Raw(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Raw(info.ID); !errors.Is(err, ErrNoRawStream) {
		t.Errorf("second claim err = %v, want ErrNoRawStream", err)
	}

	reader := bufio.NewReaderSize(raw, 1<<20)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSuffix(line, "\n"); got != payload {
		t.Errorf("raw stream lost bytes: got %d bytes, want %d", len(got), len(payload))
	}

	waitFinished(t, m, info.ID)
	diagnostics, err := m.PeekBytes(info.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics, "diagnostics") {
		t.Errorf("stderr missing from the ring buffer: %q", diagnostics)
	}
	if strings.Contains(diagnostics, "abcdefghij") {
		t.Error("the protocol stream reached the ring buffer; it must bypass it entirely")
	}
}

func TestUnitWithoutRawStreamReportsSo(t *testing.T) {
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "quiet"}, Func(func(context.Context, io.Writer) error {
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Raw(info.ID); !errors.Is(err, ErrNoRawStream) {
		t.Errorf("err = %v, want ErrNoRawStream", err)
	}
}

func TestPipeWithoutRawStdoutMergesBothStreamsIntoTheBuffer(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "both"}, Pipe(ProcOptions{
		Binary: "/bin/sh", Args: []string{"-c", "echo to-stdout; echo to-stderr >&2"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	waitFinished(t, m, info.ID)
	out, err := m.PeekBytes(info.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "to-stdout") || !strings.Contains(out, "to-stderr") {
		t.Errorf("buffer = %q, want both streams", out)
	}
}

// 7. The registry never learns what readiness means; it only runs the probe and
// reports the outcome, and a unit is not running until the probe says so.
func TestReadinessProbeGatesTheRunningState(t *testing.T) {
	m := NewManager()
	release := make(chan struct{})
	info, err := m.Start(t.Context(), Spec{Command: "slow-start"}, AttachFunc(func(ctx context.Context) (*Attached, error) {
		done := make(chan struct{})
		go func() { <-ctx.Done(); close(done) }()
		return &Attached{
			Shape: ShapeExtern,
			Wait:  func() Result { <-done; return Result{} },
			Ready: func(ctx context.Context) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if info.State != StateStarting {
		t.Fatalf("initial state = %q, want starting", info.State)
	}
	if !info.State.Active() {
		t.Error("a starting unit must count as active")
	}
	close(release)
	waitUntil(t, 5*time.Second, func() bool {
		current, _ := m.Get(info.ID)
		return current.State == StateRunning
	})
	current, _ := m.Get(info.ID)
	if current.ReadyAt.IsZero() {
		t.Error("ReadyAt not recorded")
	}
}

func TestReadinessFailureIsAFailureNotAKill(t *testing.T) {
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "never-ready"}, AttachFunc(func(ctx context.Context) (*Attached, error) {
		done := make(chan struct{})
		go func() { <-ctx.Done(); close(done) }()
		return &Attached{
			Shape: ShapeExtern,
			Wait:  func() Result { <-done; return Result{} },
			Ready: func(context.Context) error { return errors.New("port closed") },
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateFailed {
		t.Errorf("state = %q, want failed: it never started serving", final.State)
	}
	if !strings.Contains(final.Reason, "not ready") || !strings.Contains(final.Reason, "port closed") {
		t.Errorf("reason = %q, want the probe's own error", final.Reason)
	}
}

func TestLivenessFailureStopsTheUnit(t *testing.T) {
	m := NewManager()
	var checks int
	var mu sync.Mutex
	info, err := m.Start(t.Context(), Spec{Command: "wedged", LiveEvery: 20 * time.Millisecond},
		AttachFunc(func(ctx context.Context) (*Attached, error) {
			done := make(chan struct{})
			go func() { <-ctx.Done(); close(done) }()
			return &Attached{
				Shape: ShapeExtern,
				Wait:  func() Result { <-done; return Result{} },
				Live: func(context.Context) error {
					mu.Lock()
					defer mu.Unlock()
					checks++
					if checks < 3 {
						return nil
					}
					return errors.New("stopped responding")
				},
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateFailed || !strings.Contains(final.Reason, "stopped responding") {
		t.Errorf("state=%q reason=%q, want a liveness failure", final.State, final.Reason)
	}
}

func TestDialProbeWaitsForTheListener(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	socket := filepath.Join(t.TempDir(), "probe.sock")

	info, err := m.Start(t.Context(), Spec{Command: "listener"}, Pipe(ProcOptions{
		Binary: "/bin/sh",
		Args:   []string{"-c", fmt.Sprintf("sleep 0.3; touch %q; sleep 5", socket)},
		Ready:  FileProbe(socket),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if info.State != StateStarting {
		t.Fatalf("state = %q, want starting", info.State)
	}
	waitUntil(t, 10*time.Second, func() bool {
		current, _ := m.Get(info.ID)
		return current.State == StateRunning
	})
	_ = m.Stop(info.ID, StopOptions{Reason: "done", Grace: time.Second})
	waitFinished(t, m, info.ID)
}

// 8. A unit that ignores every stop path is reported as abandoned. The
// supervisor must not block on it, or the whole registry stalls.
func TestUnstoppableUnitIsReportedAbandoned(t *testing.T) {
	m := NewManager()
	m.abandonGrace = 200 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	info, err := m.Start(t.Context(), Spec{Command: "stuck"}, Func(func(context.Context, io.Writer) error {
		<-release // deliberately ignores cancellation
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(info.ID, StopOptions{Reason: ""}); err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateKilled {
		t.Errorf("state = %q, want killed", final.State)
	}
	if final.Reason != "killed by user" {
		t.Errorf("reason = %q, want the default stop reason", final.Reason)
	}
}

// 9. The write notification is installed while output is already flowing, which
// is the race the old unsynchronised assignment lost under -race.
func TestConcurrentOutputMonitorAndStopAreRaceFree(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	var events int
	var mu sync.Mutex
	m.SetOnEvent(func(Event) {
		mu.Lock()
		events++
		mu.Unlock()
	})

	info, err := m.Start(t.Context(), Spec{Command: "chatty"}, Pipe(ProcOptions{
		Binary: "/bin/sh", Args: []string{"-c", "i=0; while :; do echo line $i; i=$((i+1)); done"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _, _ = m.PeekNew(info.ID, 4096)
				_ = m.PeekOrEmpty(info.ID, 5)
			}
		}()
	}
	_ = m.MonitorFrom(t.Context(), info.ID, 0, 5*time.Millisecond, func([]byte) {})
	time.Sleep(50 * time.Millisecond)
	_ = m.Stop(info.ID, StopOptions{Reason: "enough", Grace: time.Second})
	wg.Wait()
	waitFinished(t, m, info.ID)

	mu.Lock()
	defer mu.Unlock()
	if events == 0 {
		t.Error("no events emitted")
	}
}

func TestTerminalInterruptGoesThroughTheTerminal(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	// The shell reports which signal it saw, proving the interrupt arrived as a
	// terminal keystroke rather than as a kill of the whole group.
	script := `trap 'echo caught-int; exit 0' INT; echo ready; while :; do sleep 0.05; done`
	info, err := m.Start(t.Context(), Spec{Command: "trapping"}, TTY(ProcOptions{Line: script}))
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		out, _ := m.Peek(info.ID, 20)
		return strings.Contains(out, "ready")
	})
	if err := m.Stop(info.ID, StopOptions{Reason: "interrupt", Grace: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	waitFinished(t, m, info.ID)
	out, _ := m.Peek(info.ID, 50)
	if !strings.Contains(out, "caught-int") {
		t.Errorf("output = %q, want the trap to have run", out)
	}
}

func TestTimeoutIsRecordedAsAKillWithItsReason(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	info, err := m.Start(t.Context(), Spec{Command: "slow", Timeout: 150 * time.Millisecond},
		Pipe(ProcOptions{Binary: "/bin/sh", Args: []string{"-c", "sleep 30"}}))
	if err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateKilled || final.Reason != "timeout after 150ms" {
		t.Errorf("state=%q reason=%q, want a kill naming the deadline", final.State, final.Reason)
	}
}

func TestInputReachesTheUnitAndUnsupportedInputIsRefused(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	echo, err := m.Start(t.Context(), Spec{Command: "cat"}, Pipe(ProcOptions{Binary: "cat"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(echo.ID, []byte("round-trip\n")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		out, _ := m.Peek(echo.ID, 10)
		return strings.Contains(out, "round-trip")
	})
	_ = m.Stop(echo.ID, StopOptions{Grace: time.Second})
	waitFinished(t, m, echo.ID)

	quiet, err := m.Start(t.Context(), Spec{Command: "quiet"}, externUnit(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(quiet.ID, []byte("x")); err == nil {
		t.Error("a unit without input must refuse a write")
	}
	if err := m.Resize(quiet.ID, 80, 24); err == nil {
		t.Error("a unit that is not a terminal must refuse a resize")
	}
	_ = m.Stop(quiet.ID, StopOptions{Grace: time.Second})
	waitFinished(t, m, quiet.ID)
}

func TestShutdownStopsEveryLiveUnit(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	for i := range 3 {
		if _, err := m.Start(context.Background(), Spec{Command: fmt.Sprintf("sleeper-%d", i)},
			Pipe(ProcOptions{Binary: "/bin/sh", Args: []string{"-c", "sleep 30"}})); err != nil {
			t.Fatal(err)
		}
	}
	if n := m.RunningCount(); n != 3 {
		t.Fatalf("running = %d, want 3", n)
	}
	m.Shutdown()
	waitUntil(t, 20*time.Second, func() bool { return m.RunningCount() == 0 })
	for _, info := range m.List() {
		if info.Reason != "shutdown" {
			t.Errorf("unit %s reason = %q, want shutdown", info.ID, info.Reason)
		}
	}
}

func TestOutputFileMirrorsTheDiagnosticStream(t *testing.T) {
	requireUnix(t)
	m := NewManager()
	path := filepath.Join(t.TempDir(), "unit.log")
	info, err := m.Start(t.Context(), Spec{Command: "logged", OutputFile: path},
		Pipe(ProcOptions{Binary: "/bin/sh", Args: []string{"-c", "echo persisted"}}))
	if err != nil {
		t.Fatal(err)
	}
	waitFinished(t, m, info.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("persisted")) {
		t.Errorf("log = %q, want the unit's output", data)
	}
}

func TestStartFailureLeavesNothingRegistered(t *testing.T) {
	m := NewManager()
	_, err := m.Start(t.Context(), Spec{Command: "missing"},
		Pipe(ProcOptions{Binary: filepath.Join(t.TempDir(), "does-not-exist")}))
	if err == nil {
		t.Fatal("expected a start failure")
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("err = %v, want the exec failure", err)
	}
	if n := len(m.List()); n != 0 {
		t.Errorf("registered %d units after a failed start, want 0", n)
	}
}

// A stop must reach the whole tree. A unit that backgrounded work would
// otherwise leave the grandchild running with nothing tracking it, which is the
// reason the shapes put their children in their own process group.
func TestStopCascadesToGrandchildren(t *testing.T) {
	requireUnix(t)
	m := NewManager()

	info, err := m.Start(t.Context(), Spec{Command: "parent"}, Pipe(ProcOptions{
		Binary: "/bin/sh", Args: []string{"-c", "sleep 30 & echo CHILDPID=$!; wait"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	var grandchild int
	waitUntil(t, 10*time.Second, func() bool {
		out, _ := m.Peek(info.ID, 30)
		for _, line := range strings.Split(out, "\n") {
			if pid, ok := strings.CutPrefix(strings.TrimSpace(line), "CHILDPID="); ok {
				if n, err := strconv.Atoi(pid); err == nil && n > 0 {
					grandchild = n
					return true
				}
			}
		}
		return false
	})
	if !processAlive(grandchild) {
		t.Fatalf("grandchild %d was never running", grandchild)
	}

	if err := m.Stop(info.ID, StopOptions{Reason: "cascade", Grace: 500 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, info.ID)
	if final.State != StateKilled {
		t.Errorf("state = %q, want killed", final.State)
	}
	waitUntil(t, 10*time.Second, func() bool { return !processAlive(grandchild) })
}
