package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	chatpb "github.com/chainreactors/aiscan/aop/aiscan/chat"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/aop/aopconnect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

func TestConnectJSONAndGRPCShareChatContract(t *testing.T) {
	service, pool, stop := newConnectTestService(t)
	defer stop()
	handler := NewHandler(service, pool, nil, nil, nil, "")
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connectClient := aopconnect.NewChatServiceClient(server.Client(), server.URL, connect.WithProtoJSON())
	opened, err := connectClient.OpenSession(ctx, connect.NewRequest(&aop.OpenSessionRequest{
		RequestId: "connect-open", SessionId: "connect-session", Participant: "agent-1", Title: "connect",
	}))
	if err != nil || opened.Msg.GetAccepted().GetState() != "open" {
		t.Fatalf("Connect OpenSession = %v, %v", opened, err)
	}
	watch, err := connectClient.WatchEvents(ctx, connect.NewRequest(&aop.WatchEventsRequest{SessionId: "connect-session"}))
	if err != nil {
		t.Fatal(err)
	}
	input := &aop.Message{Id: "message-client", Role: "user", Name: "operator", Content: []*aop.Content{{
		Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello over Connect"}},
	}}}
	run, err := connectClient.RunTurn(ctx, connect.NewRequest(&aop.RunTurnRequest{
		RequestId: "connect-run", SessionId: "connect-session", TurnId: "connect-turn", Input: input,
	}))
	if err != nil || run.Msg.GetAccepted().GetState() != "running" {
		t.Fatalf("Connect RunTurn = %v, %v", run, err)
	}
	var sawInput, sawEnd bool
	for watch.Receive() {
		event := watch.Msg().GetDelivery().GetEvent()
		if message := event.GetMessage(); message != nil && message.Id == input.Id {
			sawInput = proto.Equal(message, input)
		}
		if event.GetTurnEnded() != nil && event.TurnId == "connect-turn" {
			sawEnd = true
			break
		}
	}
	if err := watch.Err(); err != nil && !sawEnd {
		t.Fatal(err)
	}
	if !sawInput || !sawEnd {
		t.Fatalf("Connect stream sawInput=%v sawEnd=%v", sawInput, sawEnd)
	}

	sessionClient := chatconnect.NewSessionServiceClient(server.Client(), server.URL, connect.WithProtoJSON())
	resetRequest := &chatpb.ResetSessionRequest{
		RequestId: "reset-1", SessionId: "connect-session", NewSessionId: "connect-session-reset",
	}
	reset, err := sessionClient.ResetSession(ctx, connect.NewRequest(resetRequest))
	if err != nil || reset.Msg.GetAccepted().GetCurrent().GetSession().GetId() != "connect-session-reset" {
		t.Fatalf("ResetSession = %v, %v", reset, err)
	}
	assertResetHistory := func() {
		t.Helper()
		oldEvents, err := connectClient.ListEvents(ctx, connect.NewRequest(&aop.ListEventsRequest{SessionId: "connect-session", Limit: 100}))
		if err != nil {
			t.Fatal(err)
		}
		oldMessage, oldEnded := 0, 0
		for _, delivery := range oldEvents.Msg.Events {
			event := delivery.Event
			if event.GetMessage().GetId() == input.Id {
				oldMessage++
			}
			if event.GetSessionEnded().GetReason() == "reset" {
				oldEnded++
			}
		}
		if oldMessage != 1 || oldEnded != 1 {
			t.Fatalf("old session history message=%d reset_end=%d events=%v", oldMessage, oldEnded, oldEvents.Msg.Events)
		}
		newEvents, err := connectClient.ListEvents(ctx, connect.NewRequest(&aop.ListEventsRequest{SessionId: "connect-session-reset", Limit: 100}))
		if err != nil {
			t.Fatal(err)
		}
		started := 0
		for _, delivery := range newEvents.Msg.Events {
			event := delivery.Event
			if event.GetSessionStarted() != nil {
				started++
			}
			if event.GetMessage() != nil || event.GetTurnStarted() != nil || event.GetTurnEnded() != nil {
				t.Fatalf("reset session inherited chat history: %v", event)
			}
		}
		if started != 1 {
			t.Fatalf("new session_started count = %d, events=%v", started, newEvents.Msg.Events)
		}
	}
	assertResetHistory()
	replayedReset, err := sessionClient.ResetSession(ctx, connect.NewRequest(proto.Clone(resetRequest).(*chatpb.ResetSessionRequest)))
	if err != nil || !proto.Equal(reset.Msg, replayedReset.Msg) {
		t.Fatalf("ResetSession replay = %v, %v; want %v", replayedReset, err, reset)
	}
	assertResetHistory()

	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.InsecureSkipVerify = true //nolint:gosec // httptest certificate
	grpcConn, err := grpc.NewClient(strings.TrimPrefix(server.URL, "https://"), grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatal(err)
	}
	defer grpcConn.Close()
	grpcClient := aop.NewChatServiceClient(grpcConn)
	grpcOpened, err := grpcClient.OpenSession(ctx, &aop.OpenSessionRequest{
		RequestId: "grpc-open", SessionId: "grpc-session", Participant: "agent-1", Title: "grpc",
	})
	if err != nil || grpcOpened.GetAccepted().GetState() != "open" {
		t.Fatalf("gRPC OpenSession through Connect handler = %v, %v", grpcOpened, err)
	}
}

func TestConnectBearerAuthentication(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	defer service.Close()
	server := httptest.NewServer(NewHandler(service, nil, nil, nil, nil, "secret"))
	defer server.Close()
	client := chatconnect.NewSessionServiceClient(server.Client(), server.URL, connect.WithProtoJSON())

	if _, err := client.ListSessions(context.Background(), connect.NewRequest(&chatpb.ListSessionsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated ListSessions error = %v", err)
	}
	request := connect.NewRequest(&chatpb.ListSessionsRequest{})
	request.Header().Set("Authorization", "Bearer secret")
	if _, err := client.ListSessions(context.Background(), request); err != nil {
		t.Fatalf("authenticated ListSessions: %v", err)
	}
}

func TestExternalGoModuleConnectClientEndToEnd(t *testing.T) {
	service, pool, stop := newConnectTestService(t)
	defer stop()
	server := httptest.NewServer(NewHandler(service, pool, nil, nil, nil, "external-secret"))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	clientDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "external-go-client"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "go", "run", ".",
		"-url", server.URL,
		"-token", "external-secret",
		"-agent", "agent-1",
		"-prompt", "hello from an independent module",
		"-timeout", "30s",
	)
	command.Dir = clientDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external client failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "done") || !strings.Contains(text, "stop=completed") {
		t.Fatalf("external client output = %q", text)
	}
}

func newConnectTestService(t *testing.T) (*Service, *AgentPool, func()) {
	t.Helper()
	store, err := NewSQLiteStore(t.TempDir() + "/connect.db")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		id: "agent-1", name: "agent-1", sendCh: make(chan *transport.ServerFrame, 32), controlCh: make(chan *transport.ServerFrame, 32),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: make(map[string]struct{}),
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case frame := <-fake.sendCh:
				respondToConnectTestFrame(pool, fake, frame)
			case frame := <-fake.controlCh:
				respondToConnectTestFrame(pool, fake, frame)
			case <-stop:
				return
			}
		}
	}()
	return service, pool, func() {
		close(stop)
		service.Close()
		_ = store.Close()
	}
}

func respondToConnectTestFrame(pool *AgentPool, fake *remoteAgent, frame *transport.ServerFrame) {
	if frame == nil {
		return
	}
	switch payload := frame.Payload.(type) {
	case *transport.ServerFrame_OpenSession:
		request := payload.OpenSession
		pool.handleAgentFrame(fake, &transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_OpenSession{OpenSession: &aop.OpenSessionResponse{
			RequestId: request.RequestId, Outcome: &aop.OpenSessionResponse_Accepted{Accepted: &aop.Session{Id: request.SessionId, State: "open", Participant: request.Participant, Title: request.Title}},
		}}})
		pool.handleAgentFrame(fake, &transport.AgentFrame{Payload: &transport.AgentFrame_Event{Event: &aop.Event{
			SessionId: request.SessionId, Emitter: fake.name, Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{}},
		}}})
	case *transport.ServerFrame_RunTurn:
		request := payload.RunTurn
		pool.handleAgentFrame(fake, &transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_RunTurn{RunTurn: &aop.RunTurnResponse{
			RequestId: request.RequestId, Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{SessionId: request.SessionId, TurnId: request.TurnId, State: "running"}},
		}}})
		for _, event := range []*aop.Event{
			{SessionId: request.SessionId, TurnId: request.TurnId, Emitter: fake.name, Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}},
			{SessionId: request.SessionId, TurnId: request.TurnId, Emitter: fake.name, Payload: &aop.Event_Message{Message: proto.Clone(request.Input).(*aop.Message)}},
			{SessionId: request.SessionId, TurnId: request.TurnId, Emitter: fake.name, Payload: &aop.Event_Message{Message: &aop.Message{Id: "assistant-1", Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "done"}}}}}}},
			{SessionId: request.SessionId, TurnId: request.TurnId, Emitter: fake.name, Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}},
		} {
			pool.handleAgentFrame(fake, &transport.AgentFrame{CorrelationId: request.TurnId, Payload: &transport.AgentFrame_Event{Event: event}})
		}
	case *transport.ServerFrame_CloseSession:
		request := payload.CloseSession
		pool.handleAgentFrame(fake, &transport.AgentFrame{Payload: &transport.AgentFrame_Event{Event: &aop.Event{
			SessionId: request.SessionId, Emitter: fake.name, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: request.Reason}},
		}}})
		pool.handleAgentFrame(fake, &transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_CloseSession{CloseSession: &aop.CloseSessionResponse{
			RequestId: request.RequestId, Outcome: &aop.CloseSessionResponse_Accepted{Accepted: &aop.Session{Id: request.SessionId, State: "closed"}},
		}}})
	case *transport.ServerFrame_CancelTurn:
		request := payload.CancelTurn
		pool.handleAgentFrame(fake, &transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_CancelTurn{CancelTurn: &aop.CancelTurnResponse{
			RequestId: request.RequestId, Outcome: &aop.CancelTurnResponse_Accepted{Accepted: &aop.TurnReceipt{SessionId: request.SessionId, TurnId: request.TurnId, State: "canceled"}},
		}}})
	}
}
