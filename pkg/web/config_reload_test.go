package web

import (
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func newFakeAgent(id string, buf int) *remoteAgent {
	return &remoteAgent{
		id:        id,
		name:      id,
		sendCh:    make(chan *transport.ServerFrame, buf),
		controlCh: make(chan *transport.ServerFrame, 1),
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
	full.sendCh <- &transport.ServerFrame{Payload: &transport.ServerFrame_Exec{Exec: &transport.ExecRequest{TaskId: "busy"}}} // saturate the buffer
	pool.register(open)
	pool.register(full)

	if n := pool.BroadcastConfigReload(); n != 2 {
		t.Fatalf("notified = %d, want 2", n)
	}
	select {
	case msg := <-open.controlCh:
		if msg.GetReloadConfig() == nil {
			t.Fatalf("open agent got %+v, want reload_config", msg)
		}
	default:
		t.Fatal("open agent got no config message")
	}
	select {
	case msg := <-full.controlCh:
		if msg.GetReloadConfig() == nil {
			t.Fatalf("full agent got %+v, want reload_config", msg)
		}
	default:
		t.Fatal("full agent got no config control message")
	}
}

func TestBroadcastConfigReloadWaitsBehindCancellationFrames(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("busy-control", 1)
	agent.controlCh <- &transport.ServerFrame{Payload: &transport.ServerFrame_CancelTurn{CancelTurn: &aop.CancelTurnRequest{TurnId: "task-1"}}}
	pool.register(agent)

	if n := pool.BroadcastConfigReload(); n != 1 {
		t.Fatalf("notified = %d, want 1", n)
	}
	if msg := <-agent.controlCh; msg.GetCancelTurn().GetTurnId() != "task-1" {
		t.Fatalf("first control frame = %+v, want cancel turn", msg)
	}
	select {
	case msg := <-agent.controlCh:
		if msg.GetReloadConfig() == nil {
			t.Fatalf("queued control frame = %+v, want reload config", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("config reload was dropped behind a full cancellation queue")
	}
}

func TestHandleAgentStatusUpdate(t *testing.T) {
	pool := NewAgentPool(nil)
	a := newFakeAgent("n1", 1)
	a.runtime = &transport.AgentRuntimeInfo{Pid: 4242, Hostname: "local-1"}
	a.status = &transport.AgentStatus{Provider: "anthropic", Model: "old-model"}
	pool.register(a)

	pool.handleAgentFrame(a, &transport.AgentFrame{Payload: &transport.AgentFrame_Status{Status: &transport.AgentStatus{
		Provider: "anthropic", Model: "glm-5.2", Bound: true,
	}}})

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
	a.status = &transport.AgentStatus{Provider: "openai", Model: "old-model"}
	pool.register(a)

	pool.handleAgentFrame(a, &transport.AgentFrame{Payload: &transport.AgentFrame_ConfigReload{ConfigReload: &transport.ConfigReloadResult{
		Ok: true, Provider: "openai", Model: "deepseek-v4-pro",
	}}})
	got := a.info().Status
	if got.Provider != "openai" || got.Model != "deepseek-v4-pro" || got.ConfigError != "" {
		t.Fatalf("unexpected config result status: %+v", got)
	}

	pool.handleAgentFrame(a, &transport.AgentFrame{Payload: &transport.AgentFrame_ConfigReload{ConfigReload: &transport.ConfigReloadResult{
		Ok: false, Error: "invalid API key",
	}}})
	if got := a.info().Status; got.ConfigError != "invalid API key" {
		t.Fatalf("config error = %q", got.ConfigError)
	}
}
