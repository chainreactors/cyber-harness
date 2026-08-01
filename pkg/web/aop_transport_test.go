package web

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAgentFrameBinaryAndJSONAreEquivalent(t *testing.T) {
	original := &transport.AgentFrame{
		FrameId: "frame-1", CorrelationId: "turn-1",
		Payload: &transport.AgentFrame_Event{Event: &aop.Event{
			Id: "event-1", SessionId: "session-1", TurnId: "turn-1", Emitter: "agent-1", Seq: 7,
			Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-1", Role: "assistant", Content: []*aop.Content{
				{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}},
				{Value: &aop.Content_Media{Media: &aop.MediaContent{Kind: "image", Resource: &aop.Resource{Source: &aop.Resource_Data{Data: []byte{0, 1, 2, 255}}, MediaType: "image/png"}}}},
			}}},
		}},
	}
	binary, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromBinary := new(transport.AgentFrame)
	if err := proto.Unmarshal(binary, fromBinary); err != nil {
		t.Fatal(err)
	}
	jsonValue, err := protojson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := new(transport.AgentFrame)
	if err := protojson.Unmarshal(jsonValue, fromJSON); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fromBinary, fromJSON) {
		t.Fatalf("binary and JSON frames differ:\nbinary=%v\njson=%v", fromBinary, fromJSON)
	}
}

func TestAOPChatServiceGRPCPersistsAgentGeneratedEvents(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	defer service.Close()
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		id: "agent-1", name: "agent-1", sendCh: make(chan *transport.ServerFrame, 8),
		controlCh: make(chan *transport.ServerFrame, 8), tasks: make(map[string]chan taskResult),
		turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake

	listener := bufconn.Listen(1 << 20)
	server := NewGRPCServer("", service, pool)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := aop.NewChatServiceClient(conn)

	opened, err := client.OpenSession(ctx, &aop.OpenSessionRequest{RequestId: "open-1", SessionId: "session-1", Participant: "agent-1", Title: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.GetAccepted().GetId() != "session-1" {
		t.Fatalf("unexpected open response: %v", opened)
	}

	runInput := &aop.Message{Id: "message-1", Role: "user", Content: []*aop.Content{
		{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "inspect this"}}},
		{Value: &aop.Content_Media{Media: &aop.MediaContent{Kind: "image", Resource: &aop.Resource{Source: &aop.Resource_Data{Data: []byte{1, 2, 3}}, MediaType: "image/png"}}}},
	}}
	run, err := client.RunTurn(ctx, &aop.RunTurnRequest{
		RequestId: "run-1", SessionId: "session-1", TurnId: "turn-1",
		Input: runInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.GetAccepted().GetTurnId() != "turn-1" {
		t.Fatalf("unexpected run response: %v", run)
	}

	before, err := client.ListEvents(ctx, &aop.ListEventsRequest{SessionId: "session-1", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Events) != 0 {
		t.Fatalf("RunTurn synthesized AOP events before the agent emitted them: %v", before.Events)
	}

	pool.handleAgentFrame(fake, &transport.AgentFrame{
		CorrelationId: "turn-1",
		Payload: &transport.AgentFrame_Event{Event: &aop.Event{
			Id: "event-1", EmittedAt: timestamppb.Now(), SessionId: "session-1",
			TurnId: "turn-1", Emitter: "agent-1", Seq: 1,
			Payload: &aop.Event_Message{Message: proto.Clone(runInput).(*aop.Message)},
		}},
	})

	listed, err := client.ListEvents(ctx, &aop.ListEventsRequest{SessionId: "session-1", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found *aop.Event
	for _, item := range listed.Events {
		if item.Event.GetMessage().GetId() == "message-1" {
			found = item.Event
			break
		}
	}
	if found == nil {
		t.Fatalf("input message event was not persisted: %v", listed.Events)
	}
	if len(found.GetMessage().Content) != 2 || !proto.Equal(found.GetMessage(), &aop.Message{Id: "message-1", Role: "user", Content: []*aop.Content{
		{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "inspect this"}}},
		{Value: &aop.Content_Media{Media: &aop.MediaContent{Kind: "image", Resource: &aop.Resource{Source: &aop.Resource_Data{Data: []byte{1, 2, 3}}, MediaType: "image/png"}}}},
	}}) {
		t.Fatalf("stored message lost content: %v", found.GetMessage())
	}
}

func TestAOPRequestIDReplayDoesNotDispatchTwice(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		id: "agent-1", name: "agent-1", sendCh: make(chan *transport.ServerFrame, 8), controlCh: make(chan *transport.ServerFrame, 8),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	server := NewAOPChatServer(service)
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, &aop.OpenSessionRequest{RequestId: "open-1", SessionId: "session-1", Participant: "agent-1"}); err != nil || opened.GetAccepted() == nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	request := &aop.RunTurnRequest{
		RequestId: "run-1", SessionId: "session-1", TurnId: "turn-1",
		Input: &aop.Message{Id: "input-1", Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}}}},
	}
	first, err := server.RunTurn(ctx, request)
	if err != nil || first.GetAccepted() == nil {
		t.Fatalf("first run = %v, %v", first, err)
	}
	second, err := server.RunTurn(ctx, proto.Clone(request).(*aop.RunTurnRequest))
	if err != nil || !proto.Equal(first, second) {
		t.Fatalf("replay = %v, %v; want %v", second, err, first)
	}
	if got := len(fake.sendCh); got != 1 {
		t.Fatalf("agent frames = %d, want one run", got)
	}
	conflicting := proto.Clone(request).(*aop.RunTurnRequest)
	conflicting.Input.Content[0] = &aop.Content{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "different"}}}
	response, err := server.RunTurn(ctx, conflicting)
	if err != nil || response.GetRejected().GetCode() != "ALREADY_EXISTS" {
		t.Fatalf("conflict = %v, %v", response, err)
	}
}

func TestCancelTurnTargetsOnlyRequestedTurn(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	defer service.Close()
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		id: "agent-1", name: "agent-1", sendCh: make(chan *transport.ServerFrame, 8), controlCh: make(chan *transport.ServerFrame, 8),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	server := NewAOPChatServer(service)
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, &aop.OpenSessionRequest{RequestId: "open-1", SessionId: "session-1", Participant: fake.id}); err != nil || opened.GetAccepted() == nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	for _, turnID := range []string{"turn-1", "turn-2"} {
		response, err := server.RunTurn(ctx, &aop.RunTurnRequest{
			RequestId: "run-" + turnID, SessionId: "session-1", TurnId: turnID,
			Input: &aop.Message{Id: "message-" + turnID, Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: turnID}}}}},
		})
		if err != nil || response.GetAccepted() == nil {
			t.Fatalf("RunTurn(%s) = %v, %v", turnID, response, err)
		}
	}

	canceled, err := server.CancelTurn(ctx, &aop.CancelTurnRequest{RequestId: "cancel-1", SessionId: "session-1", TurnId: "turn-1"})
	if err != nil || canceled.GetAccepted().GetTurnId() != "turn-1" {
		t.Fatalf("CancelTurn = %v, %v", canceled, err)
	}
	select {
	case frame := <-fake.controlCh:
		request := frame.GetCancelTurn()
		if request.GetSessionId() != "session-1" || request.GetTurnId() != "turn-1" {
			t.Fatalf("cancel frame = %v", request)
		}
	default:
		t.Fatal("cancel frame was not sent")
	}
	fake.mu.Lock()
	_, firstPending := fake.tasks["turn-1"]
	_, secondPending := fake.tasks["turn-2"]
	fake.mu.Unlock()
	if firstPending || !secondPending {
		t.Fatalf("pending turns after cancel: turn-1=%v turn-2=%v", firstPending, secondPending)
	}
	events, err := store.ListAOPEvents(ctx, "session-1", 100)
	if err != nil {
		t.Fatal(err)
	}
	terminalCount := 0
	for _, event := range events {
		if event.GetTurnEnded() == nil {
			continue
		}
		terminalCount++
		if event.TurnId != "turn-1" || event.GetTurnEnded().GetStopReason() != "canceled" {
			t.Fatalf("unexpected terminal event after exact cancel: %v", event)
		}
	}
	if terminalCount != 1 {
		t.Fatalf("terminal events after exact cancel = %d, want 1", terminalCount)
	}
	if _, err := server.CancelTurn(ctx, &aop.CancelTurnRequest{RequestId: "cancel-2", SessionId: "session-1", TurnId: "turn-2"}); err != nil {
		t.Fatal(err)
	}
}

func TestAOPRequestJournalSurvivesServerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	pool.agents["agent-1"] = &remoteAgent{
		id: "agent-1", name: "agent-1", sendCh: make(chan *transport.ServerFrame, 1), controlCh: make(chan *transport.ServerFrame, 1),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	request := &aop.OpenSessionRequest{RequestId: "open-durable", SessionId: "session-1", Participant: "agent-1", Title: "original"}
	first, err := NewAOPChatServer(service).OpenSession(context.Background(), request)
	if err != nil || first.GetAccepted() == nil {
		t.Fatalf("first open = %v, %v", first, err)
	}
	service.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service = NewService(ServiceConfig{Store: store})
	defer service.Close()
	server := NewAOPChatServer(service)
	replayed, err := server.OpenSession(context.Background(), proto.Clone(request).(*aop.OpenSessionRequest))
	if err != nil || !proto.Equal(first, replayed) {
		t.Fatalf("durable replay = %v, %v; want %v", replayed, err, first)
	}
	conflict := proto.Clone(request).(*aop.OpenSessionRequest)
	conflict.Title = "different"
	rejected, err := server.OpenSession(context.Background(), conflict)
	if err != nil || rejected.GetRejected().GetCode() != "ALREADY_EXISTS" {
		t.Fatalf("durable conflict = %v, %v", rejected, err)
	}
}
