package web

import (
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type evalSink struct {
	sid       string
	found     bool
	aopEvents []*aop.Event
}

func (s *evalSink) TaskSession(string) (string, bool) { return s.sid, s.found }
func (s *evalSink) BroadcastAOPEvent(_ string, event *aop.Event) {
	s.aopEvents = append(s.aopEvents, event)
}

func TestForwardAgentEventKeepsEvalOnlyInAOP(t *testing.T) {
	sink := &evalSink{sid: "sess-eval", found: true}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	remote := &remoteAgent{nodeState: newNodeState(), nodeID: "agent-1", name: "worker"}

	event := &aop.Event{
		SessionId: "agent-session", TurnId: "turn-1", Emitter: "test-agent",
		Payload: &aop.Event_Status{Status: &aop.Status{State: types.EvalStateEnd}},
	}
	_ = types.SetEvalDetail(event, types.EvalDetail{Round: 1, Pass: true, Reason: "found SQLi"})
	_ = types.SetCompactDetail(event, types.CompactDetail{TokensBefore: 1000, TokensAfter: 400, KeptMessages: 8})
	pool.forwardAOPFrame(remote, "turn-1", event)

	if len(sink.aopEvents) == 0 {
		t.Fatal("AOP event was not forwarded")
	}
	evalDetail, ok, err := types.GetEvalDetail(sink.aopEvents[0])
	if err != nil || !ok {
		t.Fatalf("eval extension = %#v, %v, %v", sink.aopEvents[0].Extensions, ok, err)
	}
	if evalDetail.Round != 1 || !evalDetail.Pass || evalDetail.Reason != "found SQLi" {
		t.Fatalf("eval detail = %#v", evalDetail)
	}
	compactDetail, ok, err := types.GetCompactDetail(sink.aopEvents[0])
	if err != nil || !ok || compactDetail.TokensBefore != 1000 || compactDetail.KeptMessages != 8 {
		t.Fatalf("compact detail = %#v, %v, %v", compactDetail, ok, err)
	}
}

func TestForwardStandaloneScanAOPDoesNotCreateChatHistory(t *testing.T) {
	sink := &evalSink{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	event := &aop.Event{SessionId: "scan-not-chat", Emitter: "worker", Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}}
	pool.forwardAOPFrame(&remoteAgent{nodeState: newNodeState()}, "scan-not-chat", event)
	if len(sink.aopEvents) != 0 {
		t.Fatalf("standalone scan AOP was forwarded to chat history: %+v", sink.aopEvents)
	}
}

func TestForwardUncorrelatedEventForAgentOpenSession(t *testing.T) {
	sink := &evalSink{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	state := newNodeState()
	state.openSessions["session-command"] = struct{}{}
	remote := &remoteAgent{nodeState: state}
	event := &aop.Event{
		SessionId: "session-command",
		Emitter:   "worker",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: "command-result", Role: "assistant", Content: []*aop.Content{aop.Text("Session: session-command")},
		}},
	}

	pool.forwardAOPFrame(remote, "", event)

	if len(sink.aopEvents) != 1 || sink.aopEvents[0].GetMessage().GetId() != "command-result" {
		t.Fatalf("uncorrelated command event was not forwarded: %+v", sink.aopEvents)
	}
}
