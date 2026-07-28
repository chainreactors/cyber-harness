package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/aop"
	xeval "github.com/chainreactors/aiscan/core/aop/x/eval"
	"github.com/chainreactors/aiscan/core/output"
)

// A saturated subscriber buffer must never swallow a reliable terminal event.
func TestHubBroadcastReliableSurvivesBackpressure(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("s1")
	defer unsub()

	// Saturate the 64-slot buffer with droppable deltas while nobody reads.
	const bufCap = 64
	for i := 0; i < bufCap; i++ {
		h.Broadcast("s1", HubEvent{Type: "delta", Data: mustJSON(i)})
	}

	// One more droppable event has nowhere to go: it is silently dropped, never
	// blocking and never displacing a queued event.
	h.Broadcast("s1", HubEvent{Type: "delta", Data: mustJSON("overflow")})

	// A terminal event onto the same full buffer must land, evicting the oldest.
	h.Broadcast("s1", HubEvent{Type: "terminal", Data: mustJSON("done"), Reliable: true})

	drained := make([]HubEvent, 0, bufCap)
	for len(ch) > 0 {
		drained = append(drained, <-ch)
	}

	if len(drained) != bufCap {
		t.Fatalf("buffer size = %d, want %d", len(drained), bufCap)
	}

	var sawTerminal, sawOverflow bool
	for _, e := range drained {
		if e.Type == "terminal" {
			sawTerminal = true
		}
		if string(e.Data) == string(mustJSON("overflow")) {
			sawOverflow = true
		}
	}
	if !sawTerminal {
		t.Error("terminal (reliable) event was dropped under backpressure")
	}
	if sawOverflow {
		t.Error("non-reliable overflow event should have been dropped, not queued")
	}
}

// isTerminalDomainEvent is the only test of the reliability classification: the
// run-ending platform signal must qualify, or the stuck-cursor bug returns.
// Agent lifecycle terminals are AOP events and covered by isReliableAOPEvent.
func TestIsTerminalDomainEvent(t *testing.T) {
	if !isTerminalDomainEvent(DomainEventScanComplete) {
		t.Errorf("%q should be terminal (reliable)", DomainEventScanComplete)
	}
	for _, ty := range []string{DomainEventScanStarted, DomainEventScanProgress, DomainEventAgentJoined} {
		if isTerminalDomainEvent(ty) {
			t.Errorf("%q should not be terminal", ty)
		}
	}
}

func TestBroadcastAOPEventPersistsRawEnvelope(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	const sid = "sess-aop"
	createStoredSession(t, store, sid)
	event := aop.Event{
		Type:      aop.TypeMessage,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "agent-session",
		Agent:     "aiscan",
		Seq:       7,
		Data:      json.RawMessage(`{"message_id":"m-1","role":"assistant","parts":[{"type":"text","text":"hello"}]}`),
	}
	svc.BroadcastAOPEvent(sid, event)

	events, err := store.ListAOPEvents(context.Background(), sid, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("persisted AOP events = %d, want 1", len(events))
	}
	got := events[0]
	if got.Type != event.Type || got.SessionID != event.SessionID || got.Seq != event.Seq || string(got.Data) != string(event.Data) {
		t.Fatalf("persisted AOP event = %+v, want %+v", got, event)
	}
}

func TestEvalMetadataPersistsOnlyInAOP(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "sess-eval")

	event := aop.Event{
		Type: "turn.end", TS: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: "sess-eval", Agent: "aiscan", Data: json.RawMessage(`{"turn":1}`),
	}
	_ = xeval.SetDetail(&event, xeval.Detail{Round: 2, Pass: false, Reason: "needs one more verified finding"})
	svc.BroadcastAOPEvent("sess-eval", event)
	events, err := store.ListAOPEvents(context.Background(), "sess-eval", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("persisted AOP events = %d, want 1", len(events))
	}
	detail, ok, err := xeval.GetDetail(events[0])
	if err != nil || !ok {
		t.Fatalf("persisted extension = %#v, %v, %v", events[0].Ext, ok, err)
	}
	if detail.Round != 2 || detail.Pass || detail.Reason != "needs one more verified finding" {
		t.Fatalf("persisted detail = %#v", detail)
	}
}

func TestScanCompletePersistsMarkerMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})
	createStoredSession(t, store, "sess-scan")

	// A completed scan must leave a durable marker so its inline card survives a
	// timeline rebuild (reload / session switch). The heavy Result is intentionally
	// not stored — only the scan_id, which the client re-hydrates via scan_ids.
	svc.BroadcastDomainEvent("sess-scan", DomainEvent{
		Type:   DomainEventScanComplete,
		ScanID: "scan-123",
	})

	msgs, err := store.ListMessages(context.Background(), "sess-scan", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("persisted messages = %d, want 1", len(msgs))
	}
	var metadata map[string]any
	if err := json.Unmarshal(msgs[0].Metadata, &metadata); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	if metadata["event_type"] != DomainEventScanComplete || metadata["scan_id"] != "scan-123" {
		t.Fatalf("scan marker metadata = %#v", metadata)
	}

	// A marker with no scan id is meaningless — it must not create a phantom row.
	svc.BroadcastDomainEvent("sess-scan-empty", DomainEvent{Type: DomainEventScanComplete})
	empty, _ := store.ListMessages(context.Background(), "sess-scan-empty", 100)
	if len(empty) != 0 {
		t.Fatalf("empty-scanID persisted messages = %d, want 0", len(empty))
	}
}

func TestScanEventsImmediatelyReplaysStoredTerminalState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status ScanStatus
		want   string
	}{
		{name: "completed", status: StatusCompleted, want: "event: complete"},
		{name: "failed", status: StatusFailed, want: "event: error"},
		{name: "canceled", status: StatusCanceled, want: "\"status\":\"canceled\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now()
			job := &ScanJob{
				ID: "terminal-scan", Target: "127.0.0.1", Mode: "quick", Status: tc.status,
				Error: "scan failed", CreatedAt: now, UpdatedAt: now,
			}
			if tc.status == StatusCompleted {
				job.Result = &output.Result{}
			}
			if err := store.Create(context.Background(), job); err != nil {
				t.Fatal(err)
			}

			svc := NewService(ServiceConfig{Store: store})
			h := &handlerImpl{service: svc}
			req := httptest.NewRequest("GET", "/api/scans/terminal-scan/events", nil)
			req.SetPathValue("id", job.ID)
			recorder := newLockedResponseRecorder()
			done := make(chan struct{})
			go func() {
				h.scanEvents(recorder, req)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatal("terminal scan SSE did not return immediately")
			}
			if body := recorder.BodyString(); !strings.Contains(body, tc.want) {
				t.Fatalf("SSE body = %q, want %q", body, tc.want)
			}
		})
	}
}

func TestServeSSEWithSnapshotSubscribesBeforeReadingSnapshot(t *testing.T) {
	hub := NewHub()
	req := httptest.NewRequest("GET", "/events", nil)
	recorder := newLockedResponseRecorder()

	err := ServeSSEWithSnapshot(recorder, req, hub, "session-topic", func() ([]HubEvent, error) {
		hub.Broadcast("session-topic", HubEvent{
			Type: "turn.end", Data: mustJSON(map[string]string{"stop": "completed"}), Reliable: true,
		})
		return nil, nil
	}, "turn.end")
	if err != nil {
		t.Fatal(err)
	}
	if body := recorder.BodyString(); !strings.Contains(body, "event: turn.end") {
		t.Fatalf("SSE body = %q; event broadcast during snapshot was lost", body)
	}
}

func TestServeSSEWithSnapshotDropsQueuedSnapshotDuplicates(t *testing.T) {
	hub := NewHub()
	req := httptest.NewRequest("GET", "/events", nil)
	recorder := newLockedResponseRecorder()

	err := ServeSSEWithSnapshot(recorder, req, hub, "session-topic", func() ([]HubEvent, error) {
		hub.Broadcast("session-topic", HubEvent{ID: 2, Type: "aop", Data: mustJSON("duplicate")})
		hub.Broadcast("session-topic", HubEvent{ID: 3, Type: "done", Data: mustJSON("new"), Reliable: true})
		return []HubEvent{
			{ID: 1, Type: "aop", Data: mustJSON("one")},
			{ID: 2, Type: "aop", Data: mustJSON("duplicate")},
		}, nil
	}, "done")
	if err != nil {
		t.Fatal(err)
	}
	body := recorder.BodyString()
	if strings.Count(body, "id: 2\n") != 1 {
		t.Fatalf("snapshot cursor 2 was emitted more than once: %q", body)
	}
	if !strings.Contains(body, "id: 3\n") {
		t.Fatalf("new queued event was not emitted: %q", body)
	}
}
