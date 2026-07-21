package web

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

type evalSink struct {
	sid        string
	chatEvents []ChatEvent
	aopEvents  []aop.Event
}

func (s *evalSink) TaskSession(string) (string, bool) { return s.sid, true }
func (s *evalSink) BroadcastChatEvent(_ string, event ChatEvent) {
	s.chatEvents = append(s.chatEvents, event)
}
func (s *evalSink) BroadcastAOPEvent(_ string, event aop.Event) {
	s.aopEvents = append(s.aopEvents, event)
}

func TestForwardAgentEventKeepsEvalOnlyInAOP(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	remote := &remoteAgent{id: "agent-1", name: "worker", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	event := agent.Event{
		Type: agent.EventTurnEnd, Turn: 1, EvalRound: 1, EvalPass: true,
		EvalReason: "found SQLi", CompactTokensBefore: 1000,
		CompactTokensAfter: 400, CompactKeptMessages: 8,
	}
	for _, protocolEvent := range aop.FromAgentEvent(event, "test-agent") {
		protocolEvent.SessionID = "agent-session"
		payload, _ := json.Marshal(protocolEvent)
		pool.forwardAOPEvent(remote, WSMessage{Type: "aop", TaskID: "task-1", Payload: payload})
	}

	if len(sink.chatEvents) != 0 {
		t.Fatalf("AOP metadata was duplicated as chat events: %#v", sink.chatEvents)
	}
	if len(sink.aopEvents) == 0 {
		t.Fatal("AOP event was not forwarded")
	}
	ext, ok := sink.aopEvents[0].Ext["test-agent"].(map[string]any)
	if !ok {
		t.Fatalf("extension = %#v", sink.aopEvents[0].Ext)
	}
	if ext["eval_round"] != float64(1) && ext["eval_round"] != 1 {
		t.Fatalf("eval_round = %#v", ext["eval_round"])
	}
	if ext["eval_pass"] != true || ext["eval_reason"] != "found SQLi" {
		t.Fatalf("eval extension = %#v", ext)
	}
	if ext["compact_tokens_before"] != float64(1000) && ext["compact_tokens_before"] != 1000 {
		t.Fatalf("compact extension = %#v", ext)
	}
}

func TestEvalStartStillHasNoAOPProjection(t *testing.T) {
	events := aop.FromAgentEvent(agent.Event{Type: agent.EventEvalStart}, "test-agent")
	if len(events) != 0 {
		t.Fatalf("eval_start events = %#v", events)
	}
}
