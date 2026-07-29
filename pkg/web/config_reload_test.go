package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func newFakeAgent(id string, buf int) *remoteAgent {
	return &remoteAgent{
		id:        id,
		name:      id,
		sendCh:    make(chan WSMessage, buf),
		controlCh: make(chan WSMessage, 1),
		tasks:     make(map[string]chan taskResult),
		turns:     make(map[string]int),
		done:      make(chan struct{}),
	}
}

// TestBroadcastConfigReload verifies config updates use the control channel and
// are not blocked by a saturated task/output channel.
func TestBroadcastConfigReload(t *testing.T) {
	pool := NewAgentPool(nil)
	open := newFakeAgent("open", 1)
	full := newFakeAgent("full", 1)
	full.sendCh <- WSMessage{Type: "exec"} // saturate the buffer
	pool.register(open)
	pool.register(full)

	if n := pool.BroadcastConfigReload(); n != 2 {
		t.Fatalf("notified = %d, want 2", n)
	}
	select {
	case msg := <-open.controlCh:
		if msg.Type != "config" {
			t.Fatalf("open agent got %q, want config", msg.Type)
		}
	default:
		t.Fatal("open agent got no config message")
	}
	select {
	case msg := <-full.controlCh:
		if msg.Type != "config" {
			t.Fatalf("full agent got %q, want config", msg.Type)
		}
	default:
		t.Fatal("full agent got no config control message")
	}
}

func TestBroadcastConfigReloadWaitsBehindCancellationFrames(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("busy-control", 1)
	agent.controlCh <- WSMessage{Type: webproto.TypeRunCancel, TurnID: "task-1"}
	pool.register(agent)

	if n := pool.BroadcastConfigReload(); n != 1 {
		t.Fatalf("notified = %d, want 1", n)
	}
	if msg := <-agent.controlCh; msg.Type != webproto.TypeRunCancel {
		t.Fatalf("first control frame = %q, want %q", msg.Type, webproto.TypeRunCancel)
	}
	select {
	case msg := <-agent.controlCh:
		if msg.Type != "config" {
			t.Fatalf("queued control frame = %q, want config", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("config reload was dropped behind a full cancellation queue")
	}
}

func TestHandleAgentStatusUpdate(t *testing.T) {
	pool := NewAgentPool(nil)
	a := newFakeAgent("n1", 1)
	a.runtime = webproto.AgentRuntime{PID: 4242, Hostname: "local-1"}
	a.status = webproto.AgentStatus{Provider: "anthropic", Model: "old-model"}
	pool.register(a)

	payload, _ := json.Marshal(webproto.AgentStatus{Provider: "anthropic", Model: "glm-5.2", Bound: true})
	pool.handleAgentMessage(a, WSMessage{Type: "agent.status", Payload: payload})

	got := a.info().Status
	if got.Model != "glm-5.2" {
		t.Errorf("Model = %q, want glm-5.2", got.Model)
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", got.Provider)
	}
	if runtime := a.info().Runtime; runtime.Hostname != "local-1" || runtime.PID != 4242 {
		t.Errorf("runtime clobbered: Hostname=%q PID=%d", runtime.Hostname, runtime.PID)
	}
}

func TestHandleConfigReloadResultUpdatesAgentStatus(t *testing.T) {
	pool := NewAgentPool(nil)
	a := newFakeAgent("n1", 1)
	a.status = webproto.AgentStatus{Provider: "openai", Model: "old-model"}
	pool.register(a)

	payload, _ := json.Marshal(webproto.ConfigReloadResult{
		OK: true, Provider: "openai", Model: "deepseek-v4-pro",
	})
	pool.handleAgentMessage(a, WSMessage{Type: "config.result", Payload: payload})
	got := a.info().Status
	if got.Provider != "openai" || got.Model != "deepseek-v4-pro" || got.ConfigError != "" {
		t.Fatalf("unexpected config result status: %+v", got)
	}

	payload, _ = json.Marshal(webproto.ConfigReloadResult{OK: false, Error: "invalid API key"})
	pool.handleAgentMessage(a, WSMessage{Type: "config.result", Payload: payload})
	if got := a.info().Status; got.ConfigError != "invalid API key" {
		t.Fatalf("config error = %q", got.ConfigError)
	}
}
