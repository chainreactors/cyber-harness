package web

import (
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func newFakeAgent(nodeID string, buffer int) *remoteAgent {
	return &remoteAgent{
		nodeState: newNodeState(),
		nodeID: nodeID, name: nodeID, sendCh: make(chan *aop.Envelope, buffer),
		done: make(chan struct{}),
	}
}

func TestBroadcastConfigReloadUsesApplicationFIFO(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("agent", 1)
	pool.register(agent)
	config := &types.DistributeConfig{Llm: &types.LLMConfig{ActiveProfile: "primary"}}

	if n := pool.BroadcastConfigReload(config); n != 1 {
		t.Fatalf("notified = %d, want 1", n)
	}
	envelope := <-agent.sendCh
	message, err := aop.Unwrap(envelope)
	if err != nil {
		t.Fatal(err)
	}
	reload, ok := message.(*types.ReloadProtocolMessage)
	if !ok || reload.GetRequest().GetConfig().GetLlm().GetActiveProfile() != "primary" {
		t.Fatalf("reload = %T %+v", message, message)
	}
}

func TestBroadcastConfigReloadWaitsInFIFOOrder(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("busy", 1)
	cancel := aop.MustWrap("cancel", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: &aop.CancelOperation{TargetId: "task-1"}}})
	agent.sendCh <- cancel
	pool.register(agent)

	done := make(chan int, 1)
	go func() { done <- pool.BroadcastConfigReload(&types.DistributeConfig{}) }()
	select {
	case <-done:
		t.Fatal("reload bypassed the full FIFO")
	case <-time.After(50 * time.Millisecond):
	}
	if first := <-agent.sendCh; first.Id != "cancel" {
		t.Fatalf("first envelope = %+v", first)
	}
	if notified := <-done; notified != 1 {
		t.Fatalf("notified = %d", notified)
	}
	message, _ := aop.Unwrap(<-agent.sendCh)
	if reload, ok := message.(*types.ReloadProtocolMessage); !ok || reload.GetRequest() == nil {
		t.Fatalf("second message = %T", message)
	}
}

func TestHandleAgentStatusUpdate(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("n1", 1)
	agent.runtime = &aop.AgentRuntimeInfo{Pid: 4242, Hostname: "local-1"}
	agent.status = &aop.AgentStatus{Provider: "anthropic", Model: "old-model"}
	pool.register(agent)

	pool.handleAgentEnvelope(agent, aop.MustWrap("status", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: &aop.AgentStatus{
		Provider: "anthropic", Model: "glm-5.2", Bound: true,
	}}}))

	view := agent.view()
	if view.GetStatus().GetModel() != "glm-5.2" || view.GetStatus().GetProvider() != "anthropic" {
		t.Fatalf("status = %+v", view.GetStatus())
	}
	if runtime := view.GetHello().GetRuntime(); runtime.GetHostname() != "local-1" || runtime.GetPid() != 4242 {
		t.Fatalf("runtime clobbered: %+v", runtime)
	}
}

func TestHandleConfigReloadResultUpdatesAgentStatus(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("n1", 1)
	agent.status = &aop.AgentStatus{Provider: "openai", Model: "old-model"}
	pool.register(agent)

	pool.handleAgentEnvelope(agent, aop.MustWrap("reload-result", "reload", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: &types.ReloadResult{
		Ok: true, Provider: "openai", Model: "deepseek-v4-pro",
	}}}))
	if got := agent.view().GetStatus(); got.GetProvider() != "openai" || got.GetModel() != "deepseek-v4-pro" || got.GetConfigError() != "" {
		t.Fatalf("unexpected config result status: %+v", got)
	}

	pool.handleAgentEnvelope(agent, aop.MustWrap("reload-error", "reload", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: &types.ReloadResult{
		Ok: false, Error: "invalid API key",
	}}}))
	if got := agent.view().GetStatus(); got.GetConfigError() != "invalid API key" {
		t.Fatalf("config error = %q", got.GetConfigError())
	}
}
