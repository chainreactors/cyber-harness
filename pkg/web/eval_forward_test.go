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

	event := aop.Event{
		Type:      aop.TypeStatus,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "agent-session",
		Agent:     "test-agent",
		Data: mustJSON(aop.StatusData{
			State: agent.StatusEvalEnd,
		}),
		Ext: map[string]any{"test-agent": map[string]any{
			"eval_round":             1,
			"eval_pass":              true,
			"eval_reason":            "found SQLi",
			"compact_tokens_before":  1000,
			"compact_tokens_after":   400,
			"compact_kept_messages":  8,
		}},
	}
	payload, _ := json.Marshal(event)
	pool.forwardAOPEvent(remote, WSMessage{Type: "aop", TaskID: "task-1", Payload: payload})

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
