package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type lockedResponseRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func newLockedResponseRecorder() *lockedResponseRecorder {
	return &lockedResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *lockedResponseRecorder) Header() http.Header {
	return r.ResponseRecorder.Header()
}

func (r *lockedResponseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.WriteHeader(statusCode)
}

func (r *lockedResponseRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(data)
}

func (r *lockedResponseRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.Flush()
}

func (r *lockedResponseRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
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
		id: "agent-1", name: "worker", sendCh: make(chan *transport.ServerFrame, 8),
		tasks: map[string]chan taskResult{}, turns: map[string]int{},
	}
	taskCh := make(chan taskResult, 1)
	remote.tasks["task-1"] = taskCh
	pool.register(remote)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "agent-1", "replay me")
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := aop.JSONValue(map[string]string{"command": "ls"})
	stored := []*aop.Event{
		{Id: "e-1", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 1, 0, time.UTC)), SessionId: session.ID, Emitter: "aiscan",
			Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hi")}}}},
		{Id: "e-2", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 2, 0, time.UTC)), SessionId: session.ID, Emitter: "aiscan",
			Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{Id: "tc-1", Name: "bash", Arguments: arguments}}},
		{Id: "e-3", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 3, 0, time.UTC)), SessionId: session.ID, TurnId: "turn-1", Emitter: "aiscan",
			Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed"}}},
	}
	for _, event := range stored {
		if err := store.AddAOPEvent(ctx, session.ID, event); err != nil {
			t.Fatal(err)
		}
	}

	response, err := NewAOPChatServer(svc).ListEvents(ctx, &aop.ListEventsRequest{SessionId: session.ID, Limit: 100})
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
	after, err := store.ListAOPEvents(ctx, session.ID, 100)
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
	session, err := svc.CreateSession(context.Background(), "", "resume")
	if err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 3; seq++ {
		detail, _ := aop.JSONValue(map[string]int{"seq": seq})
		if err := store.AddAOPEvent(context.Background(), session.ID, &aop.Event{
			Id: string(rune('0' + seq)), EmittedAt: timestamppb.Now(), SessionId: session.ID, Emitter: "aiscan",
			Payload: &aop.Event_Status{Status: &aop.Status{State: "running", Detail: detail}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var deliveries []*aop.EventDelivery
	err = NewAOPChatServer(svc).(*aopChatServer).watchEvents(&aop.WatchEventsRequest{
		SessionId: session.ID, AfterCursor: "2",
	}, ctx, func(response *aop.WatchEventsResponse) error {
		deliveries = append(deliveries, response.Delivery)
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
