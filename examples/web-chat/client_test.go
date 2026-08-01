package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/aop/aopconnect"
)

type exampleChatHandler struct {
	aopconnect.UnimplementedChatServiceHandler
	t      *testing.T
	token  string
	ready  chan struct{}
	events chan *aop.Event
	once   sync.Once
}

func (h *exampleChatHandler) authenticate(header http.Header) {
	h.t.Helper()
	if got := header.Get("Authorization"); got != "Bearer "+h.token {
		h.t.Errorf("Authorization = %q", got)
	}
}

func (h *exampleChatHandler) OpenSession(_ context.Context, request *connect.Request[aop.OpenSessionRequest]) (*connect.Response[aop.OpenSessionResponse], error) {
	h.authenticate(request.Header())
	return connect.NewResponse(&aop.OpenSessionResponse{RequestId: request.Msg.RequestId, Outcome: &aop.OpenSessionResponse_Accepted{Accepted: &aop.Session{
		Id: request.Msg.SessionId, Participant: request.Msg.Participant, State: "open", Title: request.Msg.Title,
	}}}), nil
}

func (h *exampleChatHandler) RunTurn(ctx context.Context, request *connect.Request[aop.RunTurnRequest]) (*connect.Response[aop.RunTurnResponse], error) {
	h.authenticate(request.Header())
	select {
	case <-h.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if request.Msg.Input.GetRole() != "user" || request.Msg.Input.GetId() == "" {
		h.t.Errorf("RunTurn input = %v", request.Msg.Input)
	}
	turnID := request.Msg.TurnId
	h.events <- &aop.Event{SessionId: request.Msg.SessionId, TurnId: turnID, Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
		MessageId: "assistant-1", Value: &aop.MessageDelta_Text{Text: "hello"},
	}}}
	h.events <- &aop.Event{SessionId: request.Msg.SessionId, TurnId: turnID, Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "assistant-1", Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}}},
	}}}
	h.events <- &aop.Event{SessionId: request.Msg.SessionId, TurnId: turnID, Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
		StopReason: "completed", Usage: &aop.TokenUsage{TotalTokens: 3},
	}}}
	return connect.NewResponse(&aop.RunTurnResponse{RequestId: request.Msg.RequestId, Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: request.Msg.SessionId, TurnId: turnID, State: "running",
	}}}), nil
}

func (h *exampleChatHandler) WatchEvents(ctx context.Context, request *connect.Request[aop.WatchEventsRequest], stream *connect.ServerStream[aop.WatchEventsResponse]) error {
	h.authenticate(request.Header())
	h.once.Do(func() { close(h.ready) })
	cursor := 0
	for {
		select {
		case event := <-h.events:
			cursor++
			if err := stream.Send(&aop.WatchEventsResponse{Delivery: &aop.EventDelivery{Cursor: string(rune('0' + cursor)), Event: event}}); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestAskUsesConnectChatServiceEndToEnd(t *testing.T) {
	const token = "test-token"
	handler := &exampleChatHandler{t: t, token: token, ready: make(chan struct{}), events: make(chan *aop.Event, 8)}
	path, connectHandler := aopconnect.NewChatServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, connectHandler)
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, request *http.Request) {
		handler.authenticate(request.Header)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"agent-1","name":"worker","status":{"provider":"openai","model":"test"}}]`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var streamed strings.Builder
	result, err := client.Ask(ctx, "hello", "", func(delta string) { streamed.WriteString(delta) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello" || streamed.String() != "hello" || result.Stop != "completed" {
		t.Fatalf("result = %+v streamed=%q", result, streamed.String())
	}
	if result.SessionID == "" || result.TurnID == "" || result.AgentID != "agent-1" || result.Usage.GetTotalTokens() != 3 {
		t.Fatalf("result identity/usage = %+v", result)
	}
}

func TestAskRequiresLLMCapableAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"scanner-only","status":{}}]`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Ask(context.Background(), "hello", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no connected LLM-capable agent") {
		t.Fatalf("error = %v", err)
	}
}
