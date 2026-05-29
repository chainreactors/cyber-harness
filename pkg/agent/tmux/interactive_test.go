package tmux

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMultiRoundInteraction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	defer mgr.Shutdown()

	dir := t.TempDir()
	info, err := mgr.Create(dir, "sh", "interactive", 30*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Round 1: echo hello
	if err := mgr.Write(info.ID, []byte("echo ROUND1_HELLO\n")); err != nil {
		t.Fatalf("Write round1: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info.ID, 50)
		return strings.Contains(out, "ROUND1_HELLO")
	})
	t.Log("Round 1 passed: echo ROUND1_HELLO")

	// Round 2: create a file and cat it
	if err := mgr.Write(info.ID, []byte("echo 'content_from_round2' > /tmp/pty_test_round2.txt\n")); err != nil {
		t.Fatalf("Write round2 create: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := mgr.Write(info.ID, []byte("cat /tmp/pty_test_round2.txt\n")); err != nil {
		t.Fatalf("Write round2 cat: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info.ID, 50)
		return strings.Contains(out, "content_from_round2")
	})
	t.Log("Round 2 passed: file create + cat")

	// Round 3: use shell variable
	if err := mgr.Write(info.ID, []byte("MY_VAR=ROUND3_VALUE\n")); err != nil {
		t.Fatalf("Write round3 set var: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := mgr.Write(info.ID, []byte("echo $MY_VAR\n")); err != nil {
		t.Fatalf("Write round3 echo var: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info.ID, 50)
		return strings.Contains(out, "ROUND3_VALUE")
	})
	t.Log("Round 3 passed: shell variable persists across inputs")

	// Round 4: use PeekNew for incremental reading
	mgr.PeekNew(info.ID, 0) // drain existing output
	time.Sleep(100 * time.Millisecond)

	if err := mgr.Write(info.ID, []byte("echo INCREMENTAL_OUTPUT_42\n")); err != nil {
		t.Fatalf("Write round4: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		out, _, _ := mgr.PeekNew(info.ID, 0)
		return strings.Contains(out, "INCREMENTAL_OUTPUT_42")
	})
	t.Log("Round 4 passed: PeekNew incremental read works")

	// Round 5: interactive program (python3 REPL)
	info2, err := mgr.Create(dir, "python3 -u -i", "python-repl", 30*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create python3: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := mgr.Write(info2.ID, []byte("print(2+3)\n")); err != nil {
		t.Fatalf("Write python 2+3: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info2.ID, 50)
		return strings.Contains(out, "5")
	})

	if err := mgr.Write(info2.ID, []byte("print(10*20)\n")); err != nil {
		t.Fatalf("Write python 10*20: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info2.ID, 50)
		return strings.Contains(out, "200")
	})

	if err := mgr.Write(info2.ID, []byte("exit()\n")); err != nil {
		t.Fatalf("Write python exit: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		got, _ := mgr.Get(info2.ID)
		return got.State != StateRunning
	})
	got, _ := mgr.Get(info2.ID)
	if got.State != StateCompleted {
		t.Fatalf("python state = %s, want completed", got.State)
	}
	t.Log("Round 5 passed: interactive python3 REPL (2+3=5, 10*20=200, exit)")

	// Clean up shell session
	if err := mgr.Write(info.ID, []byte("exit\n")); err != nil {
		t.Fatalf("Write exit: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		got, _ := mgr.Get(info.ID)
		return got.State != StateRunning
	})
	t.Log("Shell session exited cleanly")
}

func TestSendCtrlC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	defer mgr.Shutdown()

	dir := t.TempDir()
	info, err := mgr.Create(dir, "sh", "ctrl-c-test", 30*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Start a long-running command
	if err := mgr.Write(info.ID, []byte("sleep 999\n")); err != nil {
		t.Fatalf("Write sleep: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Send Ctrl-C to interrupt it
	if err := mgr.Write(info.ID, []byte{0x03}); err != nil {
		t.Fatalf("Write Ctrl-C: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Shell should still be alive - send another command
	if err := mgr.Write(info.ID, []byte("echo AFTER_CTRL_C\n")); err != nil {
		t.Fatalf("Write after ctrl-c: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		out := mgr.PeekOrEmpty(info.ID, 50)
		return strings.Contains(out, "AFTER_CTRL_C")
	})
	t.Log("Ctrl-C interrupted sleep, shell survived, new command worked")

	mgr.Write(info.ID, []byte("exit\n"))
}
