package web

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

type evalSink struct {
	sid    string
	events []ChatEvent
}

func (s *evalSink) TaskSession(taskID string) (string, bool) { return s.sid, true }
func (s *evalSink) BroadcastChatEvent(sessionID string, event ChatEvent) {
	s.events = append(s.events, event)
}

func aopPayload(t *testing.T, ev agent.Event) json.RawMessage {
	t.Helper()
	aopEvents := aop.FromAgentEvent(ev, "test-agent")
	if len(aopEvents) == 0 {
		t.Fatal("FromAgentEvent produced no events")
	}
	payload, err := json.Marshal(aopEvents[0])
	if err != nil {
		t.Fatalf("marshal aop event: %v", err)
	}
	return payload
}

func TestForwardAgentEventSurfacesEvalVerdict(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "agent-1", name: "worker", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	// turn_end with eval ext
	ev := agent.Event{Type: agent.EventTurnEnd, Turn: 1, EvalRound: 1, EvalPass: true, EvalReason: "found SQLi"}
	aopEvents := aop.FromAgentEvent(ev, "test-agent")
	for _, aopEv := range aopEvents {
		payload, _ := json.Marshal(aopEv)
		pool.forwardAgentEvent(a, WSMessage{
			Type:    "aop." + aopEv.Type,
			TaskID:  "task-1",
			Payload: payload,
		})
	}

	var evalEvents []ChatEvent
	for _, e := range sink.events {
		if e.Type == ChatEventEval {
			evalEvents = append(evalEvents, e)
		}
	}
	if len(evalEvents) != 1 {
		t.Fatalf("want 1 eval event, got %d (total forwarded: %d)", len(evalEvents), len(sink.events))
	}
	got := evalEvents[0]
	if got.EvalRound != 1 || !got.EvalPass || got.EvalReason != "found SQLi" {
		t.Fatalf("verdict not carried: round=%d pass=%v reason=%q", got.EvalRound, got.EvalPass, got.EvalReason)
	}
}

func TestForwardAgentEventEvalErrorBecomesReason(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "a", name: "w", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	ev := agent.Event{Type: agent.EventTurnEnd, Turn: 1, EvalRound: 1, EvalError: "judge timed out"}
	aopEvents := aop.FromAgentEvent(ev, "test-agent")
	for _, aopEv := range aopEvents {
		payload, _ := json.Marshal(aopEv)
		pool.forwardAgentEvent(a, WSMessage{
			Type:    "aop." + aopEv.Type,
			TaskID:  "task-1",
			Payload: payload,
		})
	}

	var evalEvents []ChatEvent
	for _, e := range sink.events {
		if e.Type == ChatEventEval {
			evalEvents = append(evalEvents, e)
		}
	}
	if len(evalEvents) != 1 {
		t.Fatalf("want 1 eval event, got %d", len(evalEvents))
	}
	got := evalEvents[0]
	if got.EvalPass || got.EvalReason != "judge timed out" {
		t.Fatalf("unexpected eval error event: %+v", got)
	}
}

func TestForwardAgentEventEvalStartDropped(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "a", name: "w", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	// eval_start has no AOP mapping — FromAgentEvent returns nil
	ev := agent.Event{Type: agent.EventEvalStart, EvalRound: 0}
	aopEvents := aop.FromAgentEvent(ev, "test-agent")
	if len(aopEvents) != 0 {
		// If it does produce events, forward them and check nothing leaks
		for _, aopEv := range aopEvents {
			payload, _ := json.Marshal(aopEv)
			pool.forwardAgentEvent(a, WSMessage{
				Type:    "aop." + aopEv.Type,
				TaskID:  "task-1",
				Payload: payload,
			})
		}
	}

	var evalEvents []ChatEvent
	for _, e := range sink.events {
		if e.Type == ChatEventEval {
			evalEvents = append(evalEvents, e)
		}
	}
	if len(evalEvents) != 0 {
		t.Fatalf("eval_start should not forward eval badge, got %d events", len(evalEvents))
	}
}
