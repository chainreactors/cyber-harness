package web

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
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

// isTerminalChatEvent is the only test of the reliability classification: the
// run-ending signals must all qualify, or the stuck-cursor bug returns. Whether
// mid-stream types stay droppable is low-stakes (a mis-marked delta only adds
// eviction churn), so it isn't asserted.
func TestIsTerminalChatEvent(t *testing.T) {
	for _, ty := range []string{
		ChatEventMessage, ChatEventError,
		ChatEventScanComplete, ChatEventScanError,
	} {
		if !isTerminalChatEvent(ty) {
			t.Errorf("%q should be terminal (reliable)", ty)
		}
	}
}

// A run that ends with no final text (a tool-only turn, or an eval run that hit
// its round cap) must still broadcast the terminal message so the client
// finalizes the turn and releases the composer — but it must not leave a blank
// assistant row in the transcript. A run with real text does both.
func TestCompleteAssistantRunAlwaysSignalsButPersistsOnlyText(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	const sid = "sess-terminal"
	ch, unsub := svc.Hub().Subscribe(sessionTopic(sid))
	defer unsub()

	// Empty completion persists nothing; AOP session.end is the terminal signal.
	svc.completeAssistantRun(sid, "agent-1", "Agent One", "   ", 1)
	if got := drainEventTypes(ch); len(got) != 0 {
		t.Fatalf("empty completion broadcast = %v, want none", got)
	}
	if msgs, _ := store.ListMessages(context.Background(), sid, 100); len(msgs) != 0 {
		t.Fatalf("empty completion persisted %d messages, want 0", len(msgs))
	}

	// Text completion is persisted for the transcript but not re-broadcast as a
	// second agent protocol event.
	svc.completeAssistantRun(sid, "agent-1", "Agent One", "done", 2)
	if got := drainEventTypes(ch); len(got) != 0 {
		t.Fatalf("text completion broadcast = %v, want none", got)
	}
	msgs, _ := store.ListMessages(context.Background(), sid, 100)
	if len(msgs) != 1 || msgs[0].Content != "done" {
		t.Fatalf("text completion persisted %+v, want one message %q", msgs, "done")
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
	event := aop.Event{
		Type:      aop.TypeText,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "agent-session",
		Agent:     "aiscan",
		Seq:       7,
		Data:      json.RawMessage(`{"role":"assistant","content":"hello","delta":true}`),
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

func TestEvalEventPersistsVerdictMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	svc.BroadcastChatEvent("sess-eval", ChatEvent{
		Type:       ChatEventEval,
		EvalRound:  2,
		EvalPass:   false,
		EvalReason: "needs one more verified finding",
	})

	msgs, err := store.ListMessages(context.Background(), "sess-eval", 100)
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
	if metadata["event_type"] != ChatEventEval || metadata["eval_reason"] != "needs one more verified finding" {
		t.Fatalf("eval metadata = %#v", metadata)
	}
	if metadata["eval_round"] != float64(2) || metadata["eval_pass"] != false {
		t.Fatalf("eval verdict metadata = %#v", metadata)
	}
}

func TestScanCompletePersistsMarkerMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	// A completed scan must leave a durable marker so its inline card survives a
	// timeline rebuild (reload / session switch). The heavy Result is intentionally
	// not stored — only the scan_id, which the client re-hydrates via scan_ids.
	svc.BroadcastChatEvent("sess-scan", ChatEvent{
		Type:   ChatEventScanComplete,
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
	if metadata["event_type"] != ChatEventScanComplete || metadata["scan_id"] != "scan-123" {
		t.Fatalf("scan marker metadata = %#v", metadata)
	}

	// A marker with no scan id is meaningless — it must not create a phantom row.
	svc.BroadcastChatEvent("sess-scan-empty", ChatEvent{Type: ChatEventScanComplete})
	empty, _ := store.ListMessages(context.Background(), "sess-scan-empty", 100)
	if len(empty) != 0 {
		t.Fatalf("empty-scanID persisted messages = %d, want 0", len(empty))
	}
}

func drainEventTypes(ch <-chan HubEvent) []string {
	var out []string
	for len(ch) > 0 {
		out = append(out, (<-ch).Type)
	}
	return out
}
