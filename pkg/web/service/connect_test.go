package service

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestConnectHandlerSupportsConnectGRPCWebAndGRPC(t *testing.T) {
	service := NewService(ServiceConfig{})
	defer service.Close()

	mux := http.NewServeMux()
	registerConnectServices(mux, "", service)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	tests := []struct {
		name string
		opts []connect.ClientOption
	}{
		{name: "connect"},
		{name: "grpc-web", opts: []connect.ClientOption{connect.WithGRPCWeb()}},
		{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := rpc.NewSystemServiceClient(server.Client(), server.URL, test.opts...)
			response, err := client.GetStatus(context.Background(), connect.NewRequest(&types.GetStatusRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Msg.GetStatus() == nil {
				t.Fatal("status is missing")
			}
		})
	}
}

func TestHandlerTestConnRouting(t *testing.T) {
	svc := NewService(ServiceConfig{})
	srv := httptest.NewServer(newHandler(svc, nil, nil, ""))
	defer srv.Close()
	client := rpc.NewConfigServiceClient(srv.Client(), srv.URL)

	response, err := client.TestConnection(context.Background(), connect.NewRequest(&types.TestConnectionRequest{
		Section: "cyberhub", Config: &types.DistributeConfig{},
	}))
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if len(response.Msg.Checks) != 1 || response.Msg.Checks[0].Name != "cyberhub" {
		t.Fatalf("expected one cyberhub check, got %+v", response.Msg.Checks)
	}

	_, err = client.TestConnection(context.Background(), connect.NewRequest(&types.TestConnectionRequest{
		Section: "agent", Config: &types.DistributeConfig{},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for untestable section, got %v", err)
	}
}

func TestAOPServiceUsesSharedEnvelopeStreamOverConnectAndGRPC(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "connect-parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	defer service.Close()
	if err := store.CreateSession(context.Background(), &types.SessionRecord{
		Session: &aop.Session{Id: "session-1", State: SessionStateOpen}, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAOPEvent(context.Background(), "session-1", &aop.Event{Id: "event-1", SessionId: "session-1", Emitter: "test", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerConnectServices(mux, "", service)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	tests := []struct {
		name string
		opts []connect.ClientOption
	}{
		{name: "connect"},
		{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := rpc.NewAOPServiceClient(server.Client(), server.URL, test.opts...)
			stream := client.Connect(ctx)
			envelope, err := aop.Wrap("list-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ListEventsRequest{ListEventsRequest: &aop.ListEventsRequest{SessionId: "session-1"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(envelope); err != nil {
				t.Fatal(err)
			}
			response, err := stream.Receive()
			if err != nil {
				t.Fatal(err)
			}
			message, err := aop.Unwrap(response)
			if err != nil {
				t.Fatal(err)
			}
			core, ok := message.(*aop.ProtocolMessage)
			if !ok || len(core.GetListEventsResponse().GetEvents()) != 1 || core.GetListEventsResponse().GetEvents()[0].GetEvent().GetId() != "event-1" {
				t.Fatalf("application business response = %#v", message)
			}
			cancel()
			_ = stream.CloseRequest()
			_ = stream.CloseResponse()
		})
	}
}

// A1: a full Application Endpoint session lifecycle over Connect while the
// answering node uses the separate Node WebSocket Endpoint.
func TestConnectBidiClientSessionLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	defer service.Close()

	mux := http.NewServeMux()
	registerConnectServices(mux, "", service)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	nodeMux := http.NewServeMux()
	nodeMux.HandleFunc(NodeWebSocketPath, pool.HandleNodeWebSocket)
	nodeServer := httptest.NewServer(nodeMux)
	defer nodeServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := rpc.NewAOPServiceClient(server.Client(), server.URL)

	// Node peer: answers session.open/session.close so the client flow can
	// converge.
	agentStream := dialAgentWithIdentity(t, nodeServer, "node-1", nil, "node-1", &aop.AgentStatus{})
	defer agentStream.Close()
	go func() {
		for {
			_, raw, readErr := agentStream.ReadMessage()
			if readErr != nil {
				return
			}
			envelope := new(aop.Envelope)
			if protobuf.Unmarshal(raw, envelope) != nil {
				return
			}
			message, err := aop.Unwrap(envelope)
			if err != nil {
				return
			}
			core, ok := message.(*aop.ProtocolMessage)
			if !ok {
				continue
			}
			var reply *aop.ProtocolMessage
			if request := core.GetOpenSessionRequest(); request != nil {
				reply = &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: &aop.OpenSessionResponse{
					Outcome: &aop.OpenSessionResponse_Accepted{Accepted: &aop.Session{Id: request.SessionId, NodeId: request.NodeId, State: SessionStateOpen}},
				}}}
			}
			if request := core.GetCloseSessionRequest(); request != nil {
				reply = &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: &aop.CloseSessionResponse{
					Outcome: &aop.CloseSessionResponse_Accepted{Accepted: &aop.Session{Id: request.SessionId, State: SessionStateClosed}},
				}}}
			}
			if reply == nil {
				continue
			}
			replyEnvelope, err := aop.Wrap(fmt.Sprintf("agent-reply-%s", envelope.Id), envelope.Id, reply)
			if err != nil {
				return
			}
			raw, err = protobuf.Marshal(replyEnvelope)
			if err != nil || agentStream.WriteMessage(websocket.BinaryMessage, raw) != nil {
				return
			}
		}
	}()

	// A session bound to a node that is not connected: RunTurn must reject.
	if err := store.CreateSession(ctx, &types.SessionRecord{
		Session: &aop.Session{Id: "ghost-session", State: SessionStateOpen, NodeId: "node-ghost"}, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}

	stream := client.Connect(ctx)
	send := func(id string, message *aop.ProtocolMessage) {
		t.Helper()
		envelope, err := aop.Wrap(id, "", message)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(envelope); err != nil {
			t.Fatal(err)
		}
	}
	recv := func(wantReplyTo string) *aop.ProtocolMessage {
		t.Helper()
		envelope, err := stream.Receive()
		if err != nil {
			t.Fatalf("receive reply to %s: %v", wantReplyTo, err)
		}
		if envelope.GetReplyTo() != wantReplyTo {
			t.Fatalf("reply_to = %q, want %q", envelope.GetReplyTo(), wantReplyTo)
		}
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		core, ok := message.(*aop.ProtocolMessage)
		if !ok {
			t.Fatalf("reply to %s = %T, want core protocol message", wantReplyTo, message)
		}
		return core
	}

	send("open-1", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{NodeId: "node-1"}}})
	opened := recv("open-1").GetOpenSessionResponse().GetAccepted()
	if opened == nil || opened.GetId() == "" || opened.GetNodeId() != "node-1" {
		t.Fatalf("open accepted = %+v, want generated session on node-1", opened)
	}

	send("run-1", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: &aop.RunTurnRequest{
		SessionId: "ghost-session", Input: &aop.Message{Role: "user", Content: []*aop.Content{aop.Text("hi")}},
	}}})
	rejected := recv("run-1").GetRunTurnResponse().GetRejected()
	if rejected == nil || rejected.GetCode() != "UNAVAILABLE" {
		t.Fatalf("run rejected = %+v, want UNAVAILABLE", rejected)
	}

	send("close-1", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{SessionId: opened.GetId(), Reason: "done"}}})
	closed := recv("close-1").GetCloseSessionResponse().GetAccepted()
	if closed == nil || closed.GetState() != SessionStateClosed {
		t.Fatalf("close accepted = %+v, want closed session", closed)
	}

	cancel()
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
	_ = agentStream.Close()
}
