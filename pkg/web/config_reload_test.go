package web

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func newFakeAgent(id string, buf int) *remoteAgent {
	return &remoteAgent{
		id:     id,
		name:   id,
		sendCh: make(chan WSMessage, buf),
		tasks:  make(map[string]chan taskResult),
		turns:  make(map[string]int),
		done:   make(chan struct{}),
	}
}

// TestBroadcastConfigReload covers both branches: an open agent gets a "config"
// notification; an agent with a full send buffer is skipped, not blocked on.
func TestBroadcastConfigReload(t *testing.T) {
	pool := NewAgentPool(nil)
	open := newFakeAgent("open", 1)
	full := newFakeAgent("full", 1)
	full.sendCh <- WSMessage{Type: "exec"} // saturate the buffer
	pool.register(open)
	pool.register(full)

	if n := pool.BroadcastConfigReload(); n != 1 {
		t.Fatalf("notified = %d, want 1 (full channel skipped)", n)
	}
	select {
	case msg := <-open.sendCh:
		if msg.Type != "config" {
			t.Fatalf("open agent got %q, want config", msg.Type)
		}
	default:
		t.Fatal("open agent got no config message")
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
