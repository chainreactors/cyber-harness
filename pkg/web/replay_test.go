package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

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
			Type: aop.TypeSessionEnd, TS: "2026-07-19T00:00:03Z", SessionID: session.ID, Agent: "aiscan",
			Data: mustJSON(aop.SessionEndData{Stop: "completed", Turns: 1}),
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
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.sessionEvents(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), "session.end") {
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

	body := rec.Body.String()
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
