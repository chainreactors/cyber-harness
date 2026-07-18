package web

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
)

// evalSink is a minimal SessionLookup that maps every task to one session and
// records the ChatEvents forwarded to it.
type evalSink struct {
	sid    string
	events []ChatEvent
}

func (s *evalSink) TaskSession(taskID string) (string, bool) { return s.sid, true }
func (s *evalSink) BroadcastChatEvent(sessionID string, event ChatEvent) {
	s.events = append(s.events, event)
}

func agentEventPayload(t *testing.T, ev agent.Event) json.RawMessage {
	t.Helper()
	// Build the WS payload exactly as the agent does (webagent/agent.go): a
	// Record wrapping the Event, marshaled through Event.MarshalJSON. This makes
	// the test fail if the marshaler ever drops the verdict fields again.
	payload, err := json.Marshal(output.NewRecord(output.TypeAgent, ev))
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return payload
}

// TestForwardAgentEventSurfacesEvalVerdict guards the Goal-mode eval badge
// end-to-end through the hub: an agent.eval_end must reach the session SSE as a
// ChatEventEval carrying the round/pass/reason. The whole evaluator→hub→SSE path
// was silently dropped — Event.MarshalJSON omitted the verdict fields AND
// forwardAgentEvent had no eval case — so the per-round verdict never rendered.
func TestForwardAgentEventSurfacesEvalVerdict(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "agent-1", name: "worker", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	pool.forwardAgentEvent(a, WSMessage{
		Type:    "agent.eval_end",
		TaskID:  "task-1",
		Payload: agentEventPayload(t, agent.Event{Type: agent.EventEvalEnd, EvalRound: 1, EvalPass: true, EvalReason: "found SQLi"}),
	})

	if len(sink.events) != 1 {
		t.Fatalf("want 1 forwarded event, got %d", len(sink.events))
	}
	got := sink.events[0]
	if got.Type != ChatEventEval {
		t.Fatalf("type = %q, want %q", got.Type, ChatEventEval)
	}
	if got.EvalRound != 1 || !got.EvalPass || got.EvalReason != "found SQLi" {
		t.Fatalf("verdict not carried: round=%d pass=%v reason=%q", got.EvalRound, got.EvalPass, got.EvalReason)
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("agent id not stamped: %q", got.AgentID)
	}
}

// A judge error is still a round marker: it surfaces as a not-passed verdict
// with the error text as the reason, so the round boundary (and its badge) is
// not silently lost.
func TestForwardAgentEventEvalErrorBecomesReason(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "a", name: "w", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	pool.forwardAgentEvent(a, WSMessage{
		Type:    "agent.eval_error",
		TaskID:  "task-1",
		Payload: agentEventPayload(t, agent.Event{Type: agent.EventEvalError, EvalRound: 0, EvalError: "judge timed out"}),
	})

	if len(sink.events) != 1 {
		t.Fatalf("want 1 forwarded event, got %d", len(sink.events))
	}
	got := sink.events[0]
	if got.Type != ChatEventEval || got.EvalPass || got.EvalReason != "judge timed out" {
		t.Fatalf("unexpected eval error event: %+v", got)
	}
}

// eval_start is a transient "judging…" marker with no verdict; it must not
// produce a badge (which would render as a bogus "round 1 · not passed").
func TestForwardAgentEventEvalStartDropped(t *testing.T) {
	sink := &evalSink{sid: "sess-eval"}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	a := &remoteAgent{id: "a", name: "w", tasks: map[string]chan taskResult{}, turns: map[string]int{}}

	pool.forwardAgentEvent(a, WSMessage{
		Type:    "agent.eval_start",
		TaskID:  "task-1",
		Payload: agentEventPayload(t, agent.Event{Type: agent.EventEvalStart, EvalRound: 0}),
	})

	if len(sink.events) != 0 {
		t.Fatalf("eval_start should not forward, got %d events", len(sink.events))
	}
}
