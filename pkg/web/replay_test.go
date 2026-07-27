package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
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

// Replay (SQLite → SSE) must be a pure read: no frames to agents, no task
// lifecycle changes, no new events persisted.
func TestSessionEventsReplayHasNoSideEffects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pool := NewAgentPool(NewHub())
	svc := NewService(ServiceConfig{Store: store, AgentPool: pool})
	h := &handlerImpl{service: svc, agents: pool}

	// A connected agent with an in-flight chat task.
	remote := &remoteAgent{
		id:     "agent-1",
		name:   "worker",
		sendCh: make(chan WSMessage, 8),
		tasks:  map[string]chan taskResult{},
		turns:  map[string]int{},
	}
	taskCh := make(chan taskResult, 1)
	remote.tasks["task-1"] = taskCh
	pool.register(remote)

	ctx := context.Background()
	session, err := svc.CreateSession(ctx, "agent-1", "replay me")
	if err != nil {
		t.Fatal(err)
	}
	stored := []aop.Event{
		{
			Type: aop.TypeMessage, TS: "2026-07-19T00:00:01Z", SessionID: session.ID, Agent: "aiscan",
			Data: mustJSON(aop.MessageData{MessageID: "m-1", Role: "user", Parts: []aop.MessagePart{{Type: aop.PartText, Text: "hi"}}}),
		},
		{
			Type: aop.TypeToolCall, TS: "2026-07-19T00:00:02Z", SessionID: session.ID, Agent: "aiscan",
			Data: mustJSON(aop.ToolCallData{ToolCallID: "tc-1", ToolName: "bash", Args: map[string]string{"command": "ls"}}),
		},
		{
			Type: aop.TypeTurnEnd, TS: "2026-07-19T00:00:03Z", SessionID: session.ID, TurnID: "turn-1", Agent: "aiscan",
			Data: mustJSON(aop.TurnEndData{Stop: "completed"}),
		},
	}
	for _, ev := range stored {
		if err := store.AddAOPEvent(ctx, session.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.ListAOPEvents(ctx, session.ID, 100)
	if err != nil {
		t.Fatal(err)
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/chat/sessions/"+session.ID+"/events", nil).WithContext(reqCtx)
	req.SetPathValue("id", session.ID)
	rec := newLockedResponseRecorder()

	done := make(chan struct{})
	go func() {
		h.sessionEvents(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.BodyString(), "turn.end") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sessionEvents did not return after request cancel")
	}

	body := rec.BodyString()
	for _, ev := range stored {
		raw, _ := json.Marshal(ev.Data)
		if !strings.Contains(body, string(raw)) {
			t.Fatalf("replayed stream is missing %s event data %s", ev.Type, raw)
		}
	}

	// No agent frame was produced by the replay.
	select {
	case msg := <-remote.sendCh:
		t.Fatalf("replay dispatched a frame to the agent: %+v", msg)
	default:
	}
	// The in-flight task was not converged by replayed terminal events.
	remote.mu.Lock()
	_, stillRegistered := remote.tasks["task-1"]
	remote.mu.Unlock()
	if !stillRegistered {
		t.Fatal("replay converged the in-flight task")
	}
	select {
	case res, ok := <-taskCh:
		t.Fatalf("replay wrote to the task channel: res=%+v ok=%v", res, ok)
	default:
	}
	// Replay is read-only on the store.
	after, err := store.ListAOPEvents(ctx, session.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("event count changed by replay: before=%d after=%d", len(before), len(after))
	}
}

func TestSessionEventsResumesAfterLastEventID(t *testing.T) {
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
		if err := store.AddAOPEvent(context.Background(), session.ID, aop.Event{
			Type: aop.TypeStatus, TS: time.Now().UTC().Format(time.RFC3339Nano), SessionID: session.ID, Agent: "aiscan",
			Data: mustJSON(map[string]int{"seq": seq}),
		}); err != nil {
			t.Fatal(err)
		}
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/chat/sessions/"+session.ID+"/events", nil).WithContext(reqCtx)
	req.Header.Set("Last-Event-ID", "2")
	req.SetPathValue("id", session.ID)
	recorder := newLockedResponseRecorder()
	done := make(chan struct{})
	go func() {
		(&handlerImpl{service: svc}).sessionEvents(recorder, req)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(recorder.BodyString(), "id: 3\n") {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sessionEvents did not return after request cancel")
	}
	body := recorder.BodyString()
	if strings.Contains(body, "id: 1\n") || strings.Contains(body, "id: 2\n") || !strings.Contains(body, "id: 3\n") {
		t.Fatalf("resume body = %q, want only events after cursor 2", body)
	}
}
