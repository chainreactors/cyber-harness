package web

import (
	"context"
	"path/filepath"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestAOPEnvelopeBinaryAndJSONAreEquivalent(t *testing.T) {
	original := aop.MustWrap("frame-1", "turn-1", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: &aop.Event{
		Id: "event-1", SessionId: "session-1", TurnId: "turn-1", Emitter: "agent-1", Seq: 7,
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-1", Role: "assistant", Content: []*aop.Content{
			{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}},
			{Value: &aop.Content_Media{Media: &aop.MediaContent{Kind: "image", Resource: &aop.Resource{Source: &aop.Resource_Data{Data: []byte{0, 1, 2, 255}}, MediaType: "image/png"}}}},
		}}},
	}}})
	binary, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromBinary := new(aop.Envelope)
	if err := proto.Unmarshal(binary, fromBinary); err != nil {
		t.Fatal(err)
	}
	jsonValue, err := protojson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := new(aop.Envelope)
	if err := protojson.Unmarshal(jsonValue, fromJSON); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fromBinary, fromJSON) {
		t.Fatalf("binary and JSON frames differ:\nbinary=%v\njson=%v", fromBinary, fromJSON)
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
		id: "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 8),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	server := NewAOPChatServer(service)
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, "open-1", &aop.OpenSessionRequest{SessionId: "session-1", Participant: "agent-1"}); err != nil || opened.GetAccepted() == nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	request := &aop.RunTurnRequest{
		SessionId: "session-1", TurnId: "turn-1",
		Input: &aop.Message{Id: "input-1", Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}}}},
	}
	first, err := server.RunTurn(ctx, "run-1", request)
	if err != nil || first.GetAccepted() == nil {
		t.Fatalf("first run = %v, %v", first, err)
	}
	second, err := server.RunTurn(ctx, "run-1", proto.Clone(request).(*aop.RunTurnRequest))
	if err != nil || !proto.Equal(first, second) {
		t.Fatalf("replay = %v, %v; want %v", second, err, first)
	}
	if got := len(fake.sendCh); got != 1 {
		t.Fatalf("agent frames = %d, want one run", got)
	}
	events, err := store.ListAOPEvents(ctx, "session-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].GetMessage().GetId() != "input-1" || events[0].GetEmitter() != "aiscan.web" {
		t.Fatalf("canonical user history = %+v", events)
	}
	conflicting := proto.Clone(request).(*aop.RunTurnRequest)
	conflicting.Input.Content[0] = &aop.Content{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "different"}}}
	response, err := server.RunTurn(ctx, "run-1", conflicting)
	if err != nil || response.GetRejected().GetCode() != "ALREADY_EXISTS" {
		t.Fatalf("conflict = %v, %v", response, err)
	}
}

func TestOpenSessionLinksTypedScanExtension(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), &scanpb.Scan{
		Id: "scan-1", Target: "127.0.0.1", Mode: "quick", CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		id: "node://agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 1),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	value, err := anypb.New(&scanpb.SessionBinding{ScanId: "scan-1"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := NewAOPChatServer(service).OpenSession(context.Background(), "open-1", &aop.OpenSessionRequest{
		SessionId: "session-1", Participant: fake.id,
		Extensions: []*anypb.Any{value},
	})
	if err != nil || response.GetAccepted() == nil {
		t.Fatalf("OpenSession = %v, %v", response, err)
	}
	ids, err := store.SessionScanIDs(context.Background(), "session-1")
	if err != nil || len(ids) != 1 || ids[0] != "scan-1" {
		t.Fatalf("session scans = %v, %v", ids, err)
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
		id: "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 8),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	pool.agents[fake.id] = fake
	server := NewAOPChatServer(service)
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, "open-1", &aop.OpenSessionRequest{SessionId: "session-1", Participant: fake.id}); err != nil || opened.GetAccepted() == nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	for _, turnID := range []string{"turn-1", "turn-2"} {
		response, err := server.RunTurn(ctx, "run-"+turnID, &aop.RunTurnRequest{
			SessionId: "session-1", TurnId: turnID,
			Input: &aop.Message{Id: "message-" + turnID, Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: turnID}}}}},
		})
		if err != nil || response.GetAccepted() == nil {
			t.Fatalf("RunTurn(%s) = %v, %v", turnID, response, err)
		}
	}

	canceled, err := server.CancelTurn(ctx, "cancel-1", &aop.CancelTurnRequest{SessionId: "session-1", TurnId: "turn-1"})
	if err != nil || canceled.GetAccepted().GetTurnId() != "turn-1" {
		t.Fatalf("CancelTurn = %v, %v", canceled, err)
	}
	// The cancel shares the single FIFO with the two run dispatches; drain it
	// and locate the cancel_turn envelope.
	var request *aop.CancelTurnRequest
	drain := len(fake.sendCh)
	for i := 0; i < drain; i++ {
		message, err := aop.Unwrap(<-fake.sendCh)
		if err != nil {
			t.Fatal(err)
		}
		if core, ok := message.(*aop.ProtocolMessage); ok && core.GetCancelTurnRequest() != nil {
			request = core.GetCancelTurnRequest()
		}
	}
	if request == nil {
		t.Fatal("cancel frame was not sent")
	}
	if request.GetSessionId() != "session-1" || request.GetTurnId() != "turn-1" {
		t.Fatalf("cancel frame = %v", request)
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
	if _, err := server.CancelTurn(ctx, "cancel-2", &aop.CancelTurnRequest{SessionId: "session-1", TurnId: "turn-2"}); err != nil {
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
		id: "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 1),
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}},
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	request := &aop.OpenSessionRequest{SessionId: "session-1", Participant: "agent-1", Title: "original"}
	first, err := NewAOPChatServer(service).OpenSession(context.Background(), "open-durable", request)
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
	replayed, err := server.OpenSession(context.Background(), "open-durable", proto.Clone(request).(*aop.OpenSessionRequest))
	if err != nil || !proto.Equal(first, replayed) {
		t.Fatalf("durable replay = %v, %v; want %v", replayed, err, first)
	}
	conflict := proto.Clone(request).(*aop.OpenSessionRequest)
	conflict.Title = "different"
	rejected, err := server.OpenSession(context.Background(), "open-durable", conflict)
	if err != nil || rejected.GetRejected().GetCode() != "ALREADY_EXISTS" {
		t.Fatalf("durable conflict = %v, %v", rejected, err)
	}
}
