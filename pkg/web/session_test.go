package web

import (
	"context"
	"errors"
	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"path/filepath"
	"testing"
	"time"
)

// createTestSession inserts a session record directly through the store,
// mirroring what the agent-facing open flow would persist.
func createTestSession(t *testing.T, svc *Service, nodeID, title string) *types.SessionRecord {
	t.Helper()
	var agentName string
	if svc.agents != nil {
		if info := svc.agents.get(nodeID); info != nil {
			agentName = info.Name()
		}
	}
	now := nowProto()
	session := &types.SessionRecord{
		Session:   &aop.Session{Id: generateID(), State: SessionStateOpen, NodeId: nodeID, Title: title},
		AgentName: agentName,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := svc.store.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return session
}

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
		nodeState: &nodeState{tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}}, toolCalls: make(map[string]struct{}), childSessions: make(map[string]map[string]struct{})},
		nodeID:    "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 8),
		done: make(chan struct{}),
	}
	pool.agents[fake.nodeID] = fake
	server := service.api.Sessions
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, "open-1", &aop.OpenSessionRequest{SessionId: "session-1", NodeId: "agent-1"}); err != nil || opened.GetAccepted() == nil {
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
	if err := store.Create(context.Background(), &types.Scan{
		Id: "scan-1", Target: "127.0.0.1", Mode: "quick", CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: store})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		nodeState: &nodeState{tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}}, toolCalls: make(map[string]struct{}), childSessions: make(map[string]map[string]struct{})},
		nodeID:    "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 1),
		done: make(chan struct{}),
	}
	pool.agents[fake.nodeID] = fake
	value, err := anypb.New(&types.SessionBinding{ScanId: "scan-1"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.api.Sessions.OpenSession(context.Background(), "open-1", &aop.OpenSessionRequest{
		SessionId: "session-1", NodeId: fake.nodeID,
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
		nodeState: &nodeState{tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}}, toolCalls: make(map[string]struct{}), childSessions: make(map[string]map[string]struct{})},
		nodeID:    "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 8),
		done: make(chan struct{}),
	}
	pool.agents[fake.nodeID] = fake
	server := service.api.Sessions
	ctx := context.Background()
	if opened, err := server.OpenSession(ctx, "open-1", &aop.OpenSessionRequest{SessionId: "session-1", NodeId: fake.nodeID}); err != nil || opened.GetAccepted() == nil {
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
		nodeState: &nodeState{tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}}, toolCalls: make(map[string]struct{}), childSessions: make(map[string]map[string]struct{})},
		nodeID:    "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 1),
		done: make(chan struct{}),
	}
	request := &aop.OpenSessionRequest{SessionId: "session-1", NodeId: "agent-1", Title: "original"}
	first, err := service.api.Sessions.OpenSession(context.Background(), "open-durable", request)
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
	server := service.api.Sessions
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

// ListEvents replay is a pure read: it must not dispatch frames, converge an
// in-flight task, or append another copy of a terminal event.
func TestListEventsReplayHasNoSideEffects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pool := NewAgentPool(NewHub())
	svc := NewService(ServiceConfig{Store: store, AgentPool: pool})
	remote := &remoteAgent{
		nodeState: newNodeState(),
		nodeID:    "agent-1", name: "worker", sendCh: make(chan *aop.Envelope, 8), done: make(chan struct{}),
	}
	taskCh := make(chan taskResult, 1)
	remote.tasks["task-1"] = taskCh
	pool.register(remote)

	ctx := context.Background()
	session := createTestSession(t, svc, "agent-1", "replay me")
	arguments, _ := aop.JSONValue(map[string]string{"command": "ls"})
	stored := []*aop.Event{
		{Id: "e-1", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 1, 0, time.UTC)), SessionId: session.GetSession().GetId(), Emitter: "aiscan",
			Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hi")}}}},
		{Id: "e-2", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 2, 0, time.UTC)), SessionId: session.GetSession().GetId(), Emitter: "aiscan",
			Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{Id: "tc-1", Name: "bash", Arguments: arguments}}},
		{Id: "e-3", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 3, 0, time.UTC)), SessionId: session.GetSession().GetId(), TurnId: "turn-1", Emitter: "aiscan",
			Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}},
	}
	for _, event := range stored {
		if err := store.AddAOPEvent(ctx, session.GetSession().GetId(), event); err != nil {
			t.Fatal(err)
		}
	}

	response, err := svc.api.Sessions.ListEvents(ctx, &aop.ListEventsRequest{SessionId: session.GetSession().GetId(), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != len(stored) {
		t.Fatalf("replayed events = %d, want %d", len(response.Events), len(stored))
	}
	for index, delivery := range response.Events {
		if !proto.Equal(delivery.Event, stored[index]) {
			t.Fatalf("delivery %d = %v, want %v", index, delivery.Event, stored[index])
		}
	}
	select {
	case frame := <-remote.sendCh:
		t.Fatalf("replay dispatched a frame: %v", frame)
	default:
	}
	remote.mu.Lock()
	_, stillRegistered := remote.tasks["task-1"]
	remote.mu.Unlock()
	if !stillRegistered {
		t.Fatal("replay converged the in-flight task")
	}
	select {
	case result, ok := <-taskCh:
		t.Fatalf("replay wrote to task channel: result=%+v ok=%v", result, ok)
	default:
	}
	after, err := store.ListAOPEvents(ctx, session.GetSession().GetId(), 100)
	if err != nil || len(after) != len(stored) {
		t.Fatalf("stored events after replay = %d, %v", len(after), err)
	}
}

func TestWatchEventsResumesAfterCursor(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})
	session := createTestSession(t, svc, "", "resume")
	for seq := 1; seq <= 3; seq++ {
		if err := store.AddAOPEvent(context.Background(), session.GetSession().GetId(), &aop.Event{
			Id: string(rune('0' + seq)), EmittedAt: timestamppb.Now(), SessionId: session.GetSession().GetId(), Emitter: "aiscan",
			Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var deliveries []*aop.EventDelivery
	err = svc.api.Sessions.WatchEvents(ctx, &aop.WatchEventsRequest{
		SessionId: session.GetSession().GetId(), AfterCursor: "2",
	}, func(delivery *aop.EventDelivery) error {
		deliveries = append(deliveries, delivery)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchEvents error = %v, want context canceled", err)
	}
	if len(deliveries) != 1 || deliveries[0].Cursor != "3" || deliveries[0].Event.Id != "3" {
		t.Fatalf("resumed deliveries = %v, want only cursor 3", deliveries)
	}
}

// A2: an explicit CloseSession flips the stored session to closed and records
// a SessionEnded event on the durable AOP timeline. The agent disconnects
// before the close, so no close dispatch is attempted; the hub persists the
// terminal session event itself.
func TestCloseSessionMarksStoreClosedAndRecordsEvent(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(ServiceConfig{Store: store})
	defer service.Close()
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	fake := &remoteAgent{
		nodeState: &nodeState{tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: map[string]struct{}{"session-1": {}}, toolCalls: make(map[string]struct{}), childSessions: make(map[string]map[string]struct{})},
		nodeID:    "agent-1", name: "agent-1", sendCh: make(chan *aop.Envelope, 1),
		done: make(chan struct{}),
	}
	pool.agents[fake.nodeID] = fake

	ctx := context.Background()
	opened, err := service.api.Sessions.OpenSession(ctx, "open-1", &aop.OpenSessionRequest{SessionId: "session-1", NodeId: fake.nodeID})
	if err != nil || opened.GetAccepted() == nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	delete(pool.agents, fake.nodeID)

	closed, err := service.api.Sessions.CloseSession(ctx, "close-1", &aop.CloseSessionRequest{SessionId: "session-1", Reason: "done"})
	if err != nil || closed.GetAccepted().GetState() != SessionStateClosed {
		t.Fatalf("close = %v, %v", closed, err)
	}
	record, err := store.GetSession(ctx, "session-1")
	if err != nil || record.GetSession().GetState() != SessionStateClosed {
		t.Fatalf("stored session = %+v, %v", record, err)
	}
	events, err := store.ListAOPEvents(ctx, "session-1", 10)
	if err != nil || len(events) != 1 || events[0].GetSessionEnded().GetReason() != "done" {
		t.Fatalf("close events = %+v, %v", events, err)
	}
}

func TestHandleFileUploadCancellationRemovesPendingAgentTask(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "upload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "upload-session")
	session, err := store.GetSession(context.Background(), "upload-session")
	if err != nil {
		t.Fatal(err)
	}
	session.Session.NodeId = "upload-agent"
	if err := store.UpdateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}

	pool := NewAgentPool(NewHub())
	remote := newFakeAgent(session.GetSession().GetNodeId(), 1)
	pool.register(remote)
	svc := NewService(ServiceConfig{Store: store, AgentPool: pool})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Upload(ctx, session.GetSession().GetId(), "note.txt", []byte("hello"))
		done <- err
	}()

	var upload *aop.Envelope
	select {
	case upload = <-remote.sendCh:
	case <-time.After(time.Second):
		t.Fatal("upload was not dispatched")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload did not return after request cancellation")
	}

	message, err := aop.Unwrap(upload)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := message.(*filepb.ProtocolMessage); !ok {
		t.Fatalf("upload dispatch = %T, want file protocol message", message)
	}
	taskID := upload.GetId()
	remote.mu.Lock()
	_, pending := remote.tasks[taskID]
	remote.mu.Unlock()
	if pending {
		t.Fatal("canceled upload remained in the agent task map")
	}
	select {
	case envelope := <-remote.sendCh:
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		core, ok := message.(*aop.ProtocolMessage)
		if !ok || core.GetCancelTurnRequest().GetTurnId() != taskID {
			t.Fatalf("upload cancel envelope = %+v", message)
		}
	default:
		t.Fatal("upload cancellation was not sent to the agent")
	}
}
