package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	ptypb "github.com/chainreactors/aiscan/aop/pty"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/utils/pty"
)

func TestRuntimeOwnsPersistentMainREPLWithoutProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	option := &cfg.Option{REPLMode: "fast"}
	rt, err := NewAgentRuntime(ctx, option, telemetry.NopLogger(), &RuntimeConfig{
		ProviderOptional: true,
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

	messages := make(chan *ptypb.ProtocolMessage, 64)
	router, err := rt.newPTYRouter()
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()

	router.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl", SessionId: initial.ID,
	}}}, func(message *ptypb.ProtocolMessage) { messages <- message })
	opened := waitForPTYMessage(t, messages, time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetAttached() != nil
	})
	if opened.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("transport created a second repl: got %s want %s", opened.GetAttached().GetSession().GetId(), initial.ID)
	}

	router.Handle(ctx, ptyInput("term-repl", "/status\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
	})
	waitForPTYMessage(t, messages, 3*time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetOutput() != nil && strings.Contains(string(message.GetOutput().GetData()), "not configured")
	})

	beforeExit, _ := mgr.Get(initial.ID)
	router.Handle(ctx, ptyInput("term-repl", "/exit\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
	})
	waitForCondition(t, 3*time.Second, func() bool {
		info, ok := mgr.Get(initial.ID)
		return ok && info.State == pty.StateRunning && info.OutputBytes > beforeExit.OutputBytes
	})

	router.Handle(ctx, ptyInput("term-repl", "!tmux new-session -d -s webtask echo tmux_remote_ok\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
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
	router2, err := rt.newPTYRouter()
	if err != nil {
		t.Fatal(err)
	}
	defer router2.Close()
	reconnected := make(chan *ptypb.ProtocolMessage, 16)
	router2.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl-2", SessionId: initial.ID,
	}}}, func(message *ptypb.ProtocolMessage) {
		reconnected <- message
	})
	attached := waitForPTYMessage(t, reconnected, time.Second, func(message *ptypb.ProtocolMessage) bool {
		return message.GetAttached() != nil
	})
	if attached.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("reconnect session = %s, want %s", attached.GetAttached().GetSession().GetId(), initial.ID)
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

func TestEphemeralLocalREPLDoesNotCreateBufferedPTYConsole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rt, err := NewAgentRuntime(ctx, &cfg.Option{REPLMode: "fast"}, telemetry.NopLogger(), &RuntimeConfig{
		ProviderOptional: true,
		REPLMode:         REPLEphemeral,
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}
	defer rt.Close()

	for _, info := range rt.ptyManager.List() {
		if info.Kind == "repl" && info.Name == MainREPLName {
			t.Fatalf("ephemeral local REPL was routed through buffered PTY: %+v", info)
		}
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

func ptyInput(streamID, data string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Input{Input: &ptypb.Input{
		StreamId: streamID, Data: []byte(data),
	}}}
}

func waitForPTYMessage(t *testing.T, ch <-chan *ptypb.ProtocolMessage, timeout time.Duration, match func(*ptypb.ProtocolMessage) bool) *ptypb.ProtocolMessage {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case message := <-ch:
			if match(message) {
				return message
			}
		case <-deadline:
			t.Fatalf("timeout waiting for matching PTY message")
			return nil
		}
	}
}
