package web

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
	xcompact "github.com/chainreactors/aiscan/pkg/aop/x/compact"
	xeval "github.com/chainreactors/aiscan/pkg/aop/x/eval"
)

type evalSink struct {
	sid        string
	chatEvents []DomainEvent
	aopEvents  []aop.Event
}

func (s *evalSink) TaskSession(string) (string, bool) { return s.sid, true }
func (s *evalSink) BroadcastDomainEvent(_ string, event DomainEvent) {
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
		Data:      mustJSON(aop.StatusData{State: xeval.StateEnd}),
	}
	_ = xeval.SetDetail(&event, xeval.Detail{Round: 1, Pass: true, Reason: "found SQLi"})
	_ = xcompact.SetDetail(&event, xcompact.Detail{TokensBefore: 1000, TokensAfter: 400, KeptMessages: 8})
	payload, _ := json.Marshal(event)
	pool.forwardAOPEvent(remote, WSMessage{Type: "aop", TurnID: "turn-1", Payload: payload})

	if len(sink.chatEvents) != 0 {
		t.Fatalf("AOP metadata was duplicated as chat events: %#v", sink.chatEvents)
	}
	if len(sink.aopEvents) == 0 {
		t.Fatal("AOP event was not forwarded")
	}
	evalDetail, ok, err := xeval.GetDetail(sink.aopEvents[0])
	if err != nil || !ok {
		t.Fatalf("eval extension = %#v, %v, %v", sink.aopEvents[0].Ext, ok, err)
	}
	if evalDetail.Round != 1 || !evalDetail.Pass || evalDetail.Reason != "found SQLi" {
		t.Fatalf("eval detail = %#v", evalDetail)
	}
	compactDetail, ok, err := xcompact.GetDetail(sink.aopEvents[0])
	if err != nil || !ok || compactDetail.TokensBefore != 1000 || compactDetail.KeptMessages != 8 {
		t.Fatalf("compact detail = %#v, %v, %v", compactDetail, ok, err)
	}
}
