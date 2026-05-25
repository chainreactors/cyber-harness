package task

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type completionRecord struct {
	Info      Info
	Killed    bool
	KillCause string
}

type testCollector struct {
	mu      sync.Mutex
	records []completionRecord
	ch      chan struct{}
}

func newTestCollector() *testCollector {
	return &testCollector{ch: make(chan struct{}, 8)}
}

func (c *testCollector) observer() TaskObserver {
	return func(ev TaskEvent) {
		if ev.Kind != EventCompletion {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.records = append(c.records, completionRecord{Info: ev.TaskInfo, Killed: ev.Killed, KillCause: ev.KillCause})
		select {
		case c.ch <- struct{}{}:
		default:
		}
	}
}

func (c *testCollector) waitOne(t *testing.T, timeout time.Duration) completionRecord {
	t.Helper()
	select {
	case <-c.ch:
	case <-time.After(timeout):
		t.Fatalf("no completion received within %s", timeout)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.records[len(c.records)-1]
}

func waitUntil(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSpawnCompletesAndNotifies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	col := newTestCollector()
	mgr.SetObserver(col.observer())

	dir := t.TempDir()
	info, err := mgr.Spawn(dir, "printf done; sleep 0.05", "demo", 10*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if info.State != StateRunning {
		t.Fatalf("initial state = %s, want running", info.State)
	}

	rec := col.waitOne(t, 5*time.Second)
	formatted := FormatCompletion(rec.Info, rec.Killed, rec.KillCause, mgr.PeekOrEmpty(rec.Info.ID, 20))
	if !strings.Contains(formatted, info.ID) {
		t.Fatalf("completion missing id: %v", formatted)
	}
	if !strings.Contains(formatted, "done") {
		t.Fatalf("completion missing stdout tail: %v", formatted)
	}

	final, ok := mgr.Get(info.ID)
	if !ok {
		t.Fatal("task disappeared after completion")
	}
	if final.State != StateCompleted {
		t.Fatalf("final state = %s, want completed", final.State)
	}
	if final.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", final.ExitCode)
	}

	output, err := mgr.Peek(info.ID, 30)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if !strings.Contains(output, "done") {
		t.Fatalf("peek missing 'done': %q", output)
	}
}

func TestKillCascadesToGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	dir := t.TempDir()

	script := "sh -c 'sleep 30 & echo CHILDPID=$! ; wait'"
	info, err := mgr.Spawn(dir, script, "kill-test", 30*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	var childPID int
	waitUntil(t, 3*time.Second, func() bool {
		output, _ := mgr.Peek(info.ID, 30)
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, "CHILDPID=") {
				continue
			}
			pid := 0
			for _, c := range line[len("CHILDPID="):] {
				if c < '0' || c > '9' {
					break
				}
				pid = pid*10 + int(c-'0')
			}
			if pid > 0 {
				childPID = pid
				return true
			}
		}
		return false
	})

	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("grandchild %d already dead before Kill: %v", childPID, err)
	}

	if err := mgr.Kill(info.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	waitUntil(t, 5*time.Second, func() bool {
		final, _ := mgr.Get(info.ID)
		return final.State != StateRunning
	})
	final, _ := mgr.Get(info.ID)
	if final.State != StateKilled {
		t.Fatalf("state after Kill = %s, want killed", final.State)
	}

	waitUntil(t, 3*time.Second, func() bool {
		return syscall.Kill(childPID, 0) != nil
	})
}

func TestPeekReturnsTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	dir := t.TempDir()
	info, err := mgr.Spawn(dir, "for i in 1 2 3 4 5; do echo line$i; done", "peek-test", 5*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	waitUntil(t, 3*time.Second, func() bool {
		final, _ := mgr.Get(info.ID)
		return final.State == StateCompleted
	})

	out, err := mgr.Peek(info.ID, 3)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	want := "line3\nline4\nline5"
	if out != want {
		t.Fatalf("Peek = %q, want %q", out, want)
	}
}

func TestWaitRespectsTimeoutAndContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	defer mgr.Shutdown()

	dir := t.TempDir()
	info, err := mgr.Spawn(dir, "sleep 5", "wait-test", 30*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	start := time.Now()
	got, err := mgr.Wait(context.Background(), info.ID, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("Wait returned after %s, expected ~200ms", elapsed)
	}
	if got.State != StateRunning {
		t.Fatalf("state after short Wait = %s, want running", got.State)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
		close(cancelDone)
	}()
	_, err = mgr.Wait(ctx, info.ID, 10*time.Second)
	if err == nil {
		t.Fatal("Wait did not return error after ctx cancel")
	}
	<-cancelDone
}

func TestShutdownKillsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	dir := t.TempDir()

	info, err := mgr.Spawn(dir, "sleep 30", "shutdown-test", 60*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if syscall.Kill(info.PID, 0) != nil {
		t.Fatal("process not alive immediately after spawn")
	}

	mgr.Shutdown()

	waitUntil(t, 3*time.Second, func() bool {
		return syscall.Kill(info.PID, 0) != nil
	})
	final, _ := mgr.Get(info.ID)
	if final.State == StateRunning {
		t.Fatalf("state after Shutdown still running")
	}
}

func TestSpawnInProcessCompletesAndNotifies(t *testing.T) {
	mgr := NewManager()
	col := newTestCollector()
	mgr.SetObserver(col.observer())

	fn := func(ctx context.Context, out io.Writer) error {
		fmt.Fprintln(out, "step 1")
		fmt.Fprintln(out, "step 2")
		return nil
	}
	info, err := mgr.SpawnInProcess("in-proc", "fake-cmd arg1", 5*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}

	rec := col.waitOne(t, 3*time.Second)
	formatted := FormatCompletion(rec.Info, rec.Killed, rec.KillCause, mgr.PeekOrEmpty(rec.Info.ID, 20))
	if !strings.Contains(formatted, info.ID) {
		t.Fatalf("completion msg missing id: %v", formatted)
	}
	if !strings.Contains(formatted, "step 2") {
		t.Fatalf("completion msg missing stdout: %v", formatted)
	}

	final, _ := mgr.Get(info.ID)
	if final.State != StateCompleted {
		t.Fatalf("state = %s, want completed", final.State)
	}

	output, _ := mgr.Peek(info.ID, 30)
	if !strings.Contains(output, "step 1") {
		t.Fatalf("peek missing 'step 1': %q", output)
	}
}

func TestSpawnInProcessKillCancelsContext(t *testing.T) {
	mgr := NewManager()

	started := make(chan struct{})
	fn := func(ctx context.Context, out io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	info, err := mgr.SpawnInProcess("in-proc-kill", "blocker", 30*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}
	<-started

	if err := mgr.Kill(info.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		final, _ := mgr.Get(info.ID)
		return final.State != StateRunning
	})
	final, _ := mgr.Get(info.ID)
	if final.State != StateKilled {
		t.Fatalf("state after Kill = %s, want killed", final.State)
	}
}

func TestCompletionWithNilObserverDoesNotPanic(t *testing.T) {
	mgr := NewManager()

	fn := func(ctx context.Context, out io.Writer) error {
		fmt.Fprintln(out, "done")
		return nil
	}
	info, err := mgr.SpawnInProcess("nil-obs", "nil-obs", 5*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}
	final, err := mgr.Wait(context.Background(), info.ID, 3*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.State != StateCompleted {
		t.Fatalf("state = %s, want completed", final.State)
	}
}

func TestObserverPanicDoesNotCrashManager(t *testing.T) {
	mgr := NewManager()
	called := make(chan struct{})
	mgr.SetObserver(func(ev TaskEvent) {
		if ev.Kind == EventCompletion {
			close(called)
			panic("boom")
		}
	})

	fn := func(ctx context.Context, out io.Writer) error { return nil }
	_, err := mgr.SpawnInProcess("panic-test", "panic-test", 5*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}

	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("observer was not called")
	}
}

func TestObserverReceivesStartAndOutput(t *testing.T) {
	mgr := NewManager()
	var mu sync.Mutex
	var events []TaskEvent
	mgr.SetObserver(func(ev TaskEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	fn := func(ctx context.Context, out io.Writer) error {
		fmt.Fprintln(out, "hello")
		return nil
	}
	_, err := mgr.SpawnInProcess("events-test", "events-test", 5*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(events) < 3 {
		t.Fatalf("expected >= 3 events (start, output, completion), got %d", len(events))
	}
	if events[0].Kind != EventStart {
		t.Errorf("first event = %s, want start", events[0].Kind)
	}
	hasOutput := false
	for _, ev := range events {
		if ev.Kind == EventOutput {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		t.Error("no output event received")
	}
	if events[len(events)-1].Kind != EventCompletion {
		t.Errorf("last event = %s, want completion", events[len(events)-1].Kind)
	}
}

func TestTailLines(t *testing.T) {
	got := tailLines("a\nb\n\n\nc\n", 2)
	if got != "b\nc" {
		t.Fatalf("tailLines = %q, want %q", got, "b\nc")
	}
	got = tailLines("a", 5)
	if got != "a" {
		t.Fatalf("tailLines short = %q", got)
	}
}

func TestLabelFromCommand(t *testing.T) {
	cases := map[string]string{
		"scan -i fjbdg.com.cn --mode quick": "scan",
		"/usr/bin/python3 foo.py":           "python3",
		"   ":                               "shell",
	}
	for in, want := range cases {
		if got := labelFromCommand(in); got != want {
			t.Errorf("labelFromCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
