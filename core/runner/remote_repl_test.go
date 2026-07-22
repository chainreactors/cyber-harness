package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/utils/pty"
)

func TestRuntimeOwnsPersistentMainREPLWithoutProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Setenv("AISCAN_REPL", "fast")

	option := &cfg.Option{}
	rt, err := NewAgentRuntime(ctx, option, telemetry.NopLogger(), &RuntimeConfig{
		ProviderOptional: true,
		NoOutput:         true,
		REPLMode:         REPLPersistent,
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}
	defer rt.Close()

	mgr := rt.ptyManager
	if mgr == nil {
		t.Fatal("pty manager unavailable")
	}

	var initial pty.Info
	for _, info := range mgr.List() {
		if info.State == pty.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
			initial = info
			break
		}
	}
	if initial.ID == "" {
		t.Fatal("main-repl was not created eagerly")
	}
	if initial.Name != MainREPLName || initial.Kind != "repl" || initial.State != pty.StateRunning {
		t.Fatalf("unexpected resident repl: %+v", initial)
	}

	messages := make(chan pty.Frame, 64)
	router, err := rt.NewPTYRouter()
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()

	router.Handle(ctx, pty.Frame{
		Type:      pty.FrameAttach,
		StreamID:  "term-repl",
		SessionID: initial.ID,
	}, func(frame pty.Frame) { messages <- frame })
	opened := waitForFrame(t, messages, time.Second, func(frame pty.Frame) bool {
		if frame.Type == pty.FrameError {
			t.Fatalf("unexpected pty error: %s", frame.Error)
		}
		return frame.Type == pty.FrameAttached
	})
	if opened.SessionID != initial.ID {
		t.Fatalf("transport created a second repl: got %s want %s", opened.SessionID, initial.ID)
	}

	router.Handle(ctx, pty.Frame{Type: pty.FrameInput, StreamID: "term-repl", Data: []byte("/status\n")}, func(frame pty.Frame) {
		messages <- frame
	})
	waitForFrame(t, messages, 3*time.Second, func(frame pty.Frame) bool {
		if frame.Type == pty.FrameError {
			t.Fatalf("unexpected pty error: %s", frame.Error)
		}
		return frame.Type == pty.FrameOutput && strings.Contains(string(frame.Data), "not configured")
	})

	beforeExit, _ := mgr.Get(initial.ID)
	router.Handle(ctx, pty.Frame{Type: pty.FrameInput, StreamID: "term-repl", Data: []byte("/exit\n")}, func(frame pty.Frame) {
		messages <- frame
	})
	waitForCondition(t, 3*time.Second, func() bool {
		info, ok := mgr.Get(initial.ID)
		return ok && info.State == pty.StateRunning && info.OutputBytes > beforeExit.OutputBytes
	})

	router.Handle(ctx, pty.Frame{Type: pty.FrameInput, StreamID: "term-repl", Data: []byte("!tmux new-session -d -s webtask echo tmux_remote_ok\n")}, func(frame pty.Frame) {
		messages <- frame
	})
	waitForCondition(t, 3*time.Second, func() bool {
		for _, info := range mgr.List() {
			if info.Name == "webtask" {
				return true
			}
		}
		return false
	})

	// Closing one transport Router only detaches its monitor. A new transport
	// must reuse the same process-owned session and buffered console.
	router.Close()
	if info, ok := mgr.Get(initial.ID); !ok || info.State != pty.StateRunning {
		t.Fatalf("router close terminated resident repl: %+v ok=%v", info, ok)
	}
	router2, err := rt.NewPTYRouter()
	if err != nil {
		t.Fatal(err)
	}
	defer router2.Close()
	reconnected := make(chan pty.Frame, 16)
	router2.Handle(ctx, pty.Frame{Type: pty.FrameAttach, StreamID: "term-repl-2", SessionID: initial.ID}, func(frame pty.Frame) {
		reconnected <- frame
	})
	attached := waitForFrame(t, reconnected, time.Second, func(frame pty.Frame) bool {
		return frame.Type == pty.FrameAttached
	})
	if attached.SessionID != initial.ID {
		t.Fatalf("reconnect session = %s, want %s", attached.SessionID, initial.ID)
	}

	running := 0
	for _, info := range mgr.List() {
		if info.State == pty.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running main-repl count = %d, want 1", running)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForFrame(t *testing.T, ch <-chan pty.Frame, timeout time.Duration, match func(pty.Frame) bool) pty.Frame {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame := <-ch:
			if match(frame) {
				return frame
			}
		case <-deadline:
			t.Fatalf("timeout waiting for matching frame")
			return pty.Frame{}
		}
	}
}
