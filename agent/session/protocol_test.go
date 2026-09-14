package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/host"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

func TestCommandAdmissionRacesRuntimeClose(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	request := &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Request{Request: &types.CommandRequest{SessionId: "absent", Line: "/status"}}}
	start := make(chan struct{})
	replies := make(chan *aop.Envelope, 32)
	var callers sync.WaitGroup
	for range 32 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			envelope := aop.MustWrap(aop.EnvelopeID(), "", request)
			if err := rt.HandleCommandNamespace(context.Background(), envelope, request, func(reply *aop.Envelope) error {
				replies <- reply
				return nil
			}); err != nil {
				t.Errorf("command: %v", err)
			}
		}()
	}
	close(start)
	_ = rt.Close(context.Background())
	callers.Wait()
	if len(replies) != 32 {
		t.Fatalf("got %d replies, want 32", len(replies))
	}
}

func TestInlineHostSharesRuntimeAcrossReconnect(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	mux := aop.NewNamespaceMux(t.Context())
	if err := rt.RegisterNamespaces(mux); err != nil {
		t.Fatal(err)
	}
	open := aop.MustWrap("open", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: &aop.OpenSessionRequest{SessionId: "embedded"},
	}})
	first := host.New(mux)
	var response *aop.Envelope
	send := func(e *aop.Envelope) error { response = e; return nil }
	if err := first.Handle(open, send); err != nil {
		t.Fatal(err)
	}
	message, err := aop.Unwrap(response)
	if err != nil || message.(*aop.ProtocolMessage).GetOpenSessionResponse().GetAccepted().GetId() != "embedded" {
		t.Fatalf("open response=%v err=%v", response, err)
	}
	first.Close()
	if err := first.Handle(open, send); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed inline Host admitted a request: %v", err)
	}
	// Reconnect to the same application. Closing the communication Host must
	// neither close its session nor cancel the runtime that owns that session.
	secondMux := aop.NewNamespaceMux(t.Context())
	if err := rt.RegisterNamespaces(secondMux); err != nil {
		t.Fatal(err)
	}
	second := host.New(secondMux)
	defer second.Close()
	response = nil
	if err := second.Handle(open, send); err != nil {
		t.Fatal(err)
	}
	message, err = aop.Unwrap(response)
	if err != nil || message.(*aop.ProtocolMessage).GetOpenSessionResponse().GetAccepted().GetId() != "embedded" {
		t.Fatalf("reconnect response=%v err=%v", response, err)
	}
	if err := rt.CloseSession(context.Background(), "embedded", SessionCloseCompleted); err != nil {
		t.Fatalf("shared runtime was closed: %v", err)
	}
}

func handleRuntimeMessage(t *testing.T, rt *Runtime, id string, message protobuf.Message) *aop.Envelope {
	t.Helper()
	request := aop.MustWrap(id, "", message)
	var response *aop.Envelope
	mux := aop.NewNamespaceMux(t.Context())
	if err := rt.RegisterNamespaces(mux); err != nil {
		t.Fatal(err)
	}
	h := host.New(mux)
	defer h.Close()
	if err := h.Handle(request, func(envelope *aop.Envelope) error { response = envelope; return nil }); err != nil {
		t.Fatalf("message was not handled: %v", err)
	}
	return response
}

func TestEnvelopeErrorsKeepDistinctReplyIDs(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)

	runResponse := handleRuntimeMessage(t, rt, "turn-correlation", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: &aop.RunTurnRequest{}}})
	runMessage, _ := aop.Unwrap(runResponse)
	if runResponse.ReplyTo != "turn-correlation" || runMessage.(*aop.ProtocolMessage).GetRunTurnResponse().GetRejected() == nil {
		t.Fatalf("run response = %+v", runResponse)
	}

	commandResponse := handleRuntimeMessage(t, rt, "command-correlation", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Request{Request: &types.CommandRequest{}}})
	commandMessage, _ := aop.Unwrap(commandResponse)
	if commandResponse.ReplyTo != "command-correlation" || commandMessage.(*aop.ProtocolMessage).GetProtocolError() == nil {
		t.Fatalf("command response = %+v", commandResponse)
	}
}

func TestEnvelopeRequiresTurnID(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	response := handleRuntimeMessage(t, rt, "run-1", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: &aop.RunTurnRequest{
		SessionId: "session-1", Input: &aop.Message{Role: "user"},
	}}})
	message, _ := aop.Unwrap(response)
	rejected := message.(*aop.ProtocolMessage).GetRunTurnResponse().GetRejected()
	if rejected == nil || !strings.Contains(rejected.Message, "turn_id") {
		t.Fatalf("response = %+v", response)
	}
}

func TestEnvelopeSessionOpenIsIdempotent(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	message := &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{SessionId: "session-1"}}}
	for i := 0; i < 2; i++ {
		response := handleRuntimeMessage(t, rt, "open-1", message)
		decoded, _ := aop.Unwrap(response)
		if decoded.(*aop.ProtocolMessage).GetOpenSessionResponse().GetAccepted().GetId() != "session-1" {
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
