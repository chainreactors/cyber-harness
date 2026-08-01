package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func TestServerFrameErrorsKeepDistinctCorrelationIDs(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)

	var runResponse *transport.AgentFrame
	if !rt.HandleServerFrame(context.Background(), &transport.ServerFrame{
		CorrelationId: "turn-correlation",
		Payload:       &transport.ServerFrame_RunTurn{RunTurn: &aop.RunTurnRequest{RequestId: "run-1"}},
	}, func(frame *transport.AgentFrame) { runResponse = frame }) {
		t.Fatal("run frame was not handled")
	}
	if runResponse.CorrelationId != "turn-correlation" || runResponse.GetRunTurn().GetRejected() == nil {
		t.Fatalf("run response = %+v", runResponse)
	}

	var commandResponse *transport.AgentFrame
	if !rt.HandleServerFrame(context.Background(), &transport.ServerFrame{
		CorrelationId: "command-correlation",
		Payload: &transport.ServerFrame_Command{Command: &transport.CommandRequest{
			TaskId: "command-1",
		}},
	}, func(frame *transport.AgentFrame) { commandResponse = frame }) {
		t.Fatal("command frame was not handled")
	}
	if commandResponse.CorrelationId != "command-correlation" || commandResponse.GetOperationError().GetTaskId() != "command-1" {
		t.Fatalf("command response = %+v", commandResponse)
	}
}

func TestServerFrameRequiresTurnID(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	var response *transport.AgentFrame
	rt.HandleServerFrame(context.Background(), &transport.ServerFrame{
		Payload: &transport.ServerFrame_RunTurn{RunTurn: &aop.RunTurnRequest{
			RequestId: "run-1", SessionId: "session-1", Input: &aop.Message{Role: "user"},
		}},
	}, func(frame *transport.AgentFrame) { response = frame })
	rejected := response.GetRunTurn().GetRejected()
	if rejected == nil || !strings.Contains(rejected.Message, "turn_id") {
		t.Fatalf("response = %+v", response)
	}
}

func TestServerFrameSessionOpenIsIdempotent(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	request := &transport.ServerFrame{
		CorrelationId: "open-1",
		Payload: &transport.ServerFrame_OpenSession{OpenSession: &aop.OpenSessionRequest{
			RequestId: "open-1", SessionId: "session-1",
		}},
	}
	for i := 0; i < 2; i++ {
		var response *transport.AgentFrame
		if !rt.HandleServerFrame(context.Background(), request, func(frame *transport.AgentFrame) { response = frame }) {
			t.Fatal("session open was not handled")
		}
		if response.GetOpenSession().GetAccepted().GetId() != "session-1" {
			t.Fatalf("open %d response = %+v", i, response)
		}
	}
}

func TestCancelAOPTurnRequiresMatchingSession(t *testing.T) {
	provider := &runtimeSemanticProvider{started: make(chan struct{}), release: make(chan struct{})}
	rt := newBareRuntime(t, nil, provider)
	defer close(provider.release)
	if response := rt.OpenAOPSession(&aop.OpenSessionRequest{SessionId: "session-1"}); response.GetAccepted() == nil {
		t.Fatalf("open = %v", response)
	}
	run := rt.RunAOPTurn(context.Background(), &aop.RunTurnRequest{
		SessionId: "session-1", TurnId: "turn-1",
		Input: &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}}}},
	})
	if run.GetAccepted() == nil {
		t.Fatalf("run = %v", run)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	wrong := rt.CancelAOPTurn(&aop.CancelTurnRequest{SessionId: "session-2", TurnId: "turn-1"})
	if wrong.GetRejected().GetCode() != "NOT_FOUND" {
		t.Fatalf("wrong-session cancel = %v", wrong)
	}
	rt.mu.RLock()
	stillActive := rt.runs["turn-1"] != nil
	rt.mu.RUnlock()
	if !stillActive {
		t.Fatal("wrong-session cancel stopped the turn")
	}
	matched := rt.CancelAOPTurn(&aop.CancelTurnRequest{SessionId: "session-1", TurnId: "turn-1"})
	if matched.GetAccepted().GetTurnId() != "turn-1" {
		t.Fatalf("matching cancel = %v", matched)
	}
}
