package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type exampleSessionService struct {
	rpc.UnimplementedSessionServiceHandler
	t *testing.T
}

func (s exampleSessionService) ListSessions(_ context.Context, request *connect.Request[types.ListSessionsRequest]) (*connect.Response[types.ListSessionsResponse], error) {
	if got := request.Header().Get("Authorization"); got != "Bearer demo" {
		s.t.Fatalf("Authorization = %q", got)
	}
	return connect.NewResponse(&types.ListSessionsResponse{Sessions: []*types.SessionRecord{
		{Session: &aop.Session{Id: "session-1", State: "open", NodeId: "local", Title: "example"}},
	}}), nil
}

func (s exampleSessionService) ListEvents(_ context.Context, request *connect.Request[aop.ListEventsRequest]) (*connect.Response[aop.ListEventsResponse], error) {
	if request.Msg.GetSessionId() != "session-1" {
		s.t.Fatalf("session_id = %q", request.Msg.GetSessionId())
	}
	return connect.NewResponse(&aop.ListEventsResponse{Events: []*aop.EventDelivery{
		{Cursor: "1", Event: &aop.Event{SessionId: "session-1", Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text("hello")}}}}},
	}}), nil
}

func newExampleServer(t *testing.T) *httptest.Server {
	t.Helper()
	path, handler := rpc.NewSessionServiceHandler(exampleSessionService{t: t})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return httptest.NewServer(mux)
}

func TestRunListsSessions(t *testing.T) {
	server := newExampleServer(t)
	defer server.Close()
	var out bytes.Buffer
	if err := run(context.Background(), &out, server.Client(), server.URL, "demo", "", 10); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), `"session-1"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunListsEvents(t *testing.T) {
	server := newExampleServer(t)
	defer server.Close()
	var out bytes.Buffer
	if err := run(context.Background(), &out, server.Client(), server.URL, "demo", "session-1", 10); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), `"cursor"`) || !strings.Contains(out.String(), `"hello"`) {
		t.Fatalf("output = %s", out.String())
	}
}
