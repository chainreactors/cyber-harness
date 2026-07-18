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

// TestHandleAgentIdentityUpdate covers the post-hot-reload identity re-announce:
// the agent's swapped provider/model reach the pool (so the UI badge tracks the
// live model), while the register-time identity fields (NodeName/PID/host) are
// preserved rather than clobbered by the partial update.
func TestHandleAgentIdentityUpdate(t *testing.T) {
	pool := NewAgentPool(nil)
	a := newFakeAgent("n1", 1)
	a.identity = webproto.AgentIdentity{NodeName: "local-1", PID: 4242, Provider: "anthropic", Model: "old-model"}
	pool.register(a)

	payload, _ := json.Marshal(webproto.AgentIdentity{Provider: "anthropic", Model: "glm-5.2"})
	pool.handleAgentMessage(a, WSMessage{Type: "agent.identity", Payload: payload})

	got := a.info().Identity
	if got.Model != "glm-5.2" {
		t.Errorf("Model = %q, want glm-5.2 (identity should track the hot-reload)", got.Model)
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", got.Provider)
	}
	if got.NodeName != "local-1" || got.PID != 4242 {
		t.Errorf("register-time identity clobbered: NodeName=%q PID=%d", got.NodeName, got.PID)
	}
}
