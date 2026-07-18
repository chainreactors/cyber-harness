package web

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// A saturated subscriber buffer must never swallow a terminal event: the hub
// evicts the oldest queued (droppable) delta to make room. This is the fix that
// keeps a finished run from stranding the composer as "busy" with a blinking
// cursor when the closing message_end / message is lost to backpressure.
func TestHubBroadcastReliableSurvivesBackpressure(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("s1")
	defer unsub()

	// Saturate the 64-slot buffer with droppable deltas while nobody reads.
	const bufCap = 64
	for i := 0; i < bufCap; i++ {
		h.Broadcast("s1", HubEvent{Type: ChatEventMessageDelta, Data: mustJSON(i)})
	}

	// One more droppable event has nowhere to go: it is silently dropped, never
	// blocking and never displacing a queued event.
	h.Broadcast("s1", HubEvent{Type: ChatEventMessageDelta, Data: mustJSON("overflow")})

	// A terminal event onto the same full buffer must land, evicting the oldest.
	h.Broadcast("s1", HubEvent{Type: ChatEventMessageEnd, Data: mustJSON("done"), Reliable: true})

	drained := make([]HubEvent, 0, bufCap)
	for len(ch) > 0 {
		drained = append(drained, <-ch)
	}

	if len(drained) != bufCap {
		t.Fatalf("buffer size = %d, want %d", len(drained), bufCap)
	}

	var sawTerminal, sawOverflow bool
	for _, e := range drained {
		if e.Type == ChatEventMessageEnd {
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
		ChatEventMessage, ChatEventMessageEnd, ChatEventError,
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

	// Empty completion: broadcasts the terminal signal, persists nothing.
	svc.completeAssistantRun(sid, "agent-1", "Agent One", "   ", 1)
	if got := drainEventTypes(ch); len(got) != 1 || got[0] != ChatEventMessage {
		t.Fatalf("empty completion broadcast = %v, want one %q", got, ChatEventMessage)
	}
	if msgs, _ := store.ListMessages(context.Background(), sid, 100); len(msgs) != 0 {
		t.Fatalf("empty completion persisted %d messages, want 0", len(msgs))
	}

	// Text completion: same terminal signal, plus the reply is persisted.
	svc.completeAssistantRun(sid, "agent-1", "Agent One", "done", 2)
	if got := drainEventTypes(ch); len(got) != 1 || got[0] != ChatEventMessage {
		t.Fatalf("text completion broadcast = %v, want one %q", got, ChatEventMessage)
	}
	msgs, _ := store.ListMessages(context.Background(), sid, 100)
	if len(msgs) != 1 || msgs[0].Content != "done" {
		t.Fatalf("text completion persisted %+v, want one message %q", msgs, "done")
	}
}

// A run's intermediate assistant text — the commentary the model streams before
// its tool calls — must be persisted, not just streamed. Before this, only the
// final aggregate reply (completeAssistantRun) survived, so every earlier turn's
// text vanished from any timeline rebuilt from the store: a page reload, an SSE
// reconnect, or a session switch that revalidates against it. It is persisted as
// an assistant message carrying its turn, so buildTimelineFromMessages keys it to
// the right bubble. Streaming partials (message_start / message_delta) stay out.
func TestMessageEndPersistsIntermediateAssistantText(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	const sid = "sess-msgend"

	// Streaming partials of the same text: live-only, never persisted.
	svc.BroadcastChatEvent(sid, ChatEvent{Type: ChatEventMessageStart, Role: "assistant", Content: "有意", Turn: 1})
	svc.BroadcastChatEvent(sid, ChatEvent{Type: ChatEventMessageDelta, Role: "assistant", Content: "有意思", Turn: 1})
	// Finalized turn-1 commentary: persisted so a rebuild can show it.
	svc.BroadcastChatEvent(sid, ChatEvent{Type: ChatEventMessageEnd, Role: "assistant", Content: "有意思！charge.js 暴露了内部 API", Turn: 1})
	// Whitespace-only end (a tool-only turn): nothing to persist.
	svc.BroadcastChatEvent(sid, ChatEvent{Type: ChatEventMessageEnd, Role: "assistant", Content: "  \n ", Turn: 2})

	msgs, err := store.ListMessages(context.Background(), sid, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("persisted messages = %d, want 1 (only the non-empty message_end)", len(msgs))
	}
	got := msgs[0]
	if got.Role != "assistant" || got.Content != "有意思！charge.js 暴露了内部 API" {
		t.Fatalf("persisted message = {role:%q content:%q}, want assistant commentary", got.Role, got.Content)
	}
	var metadata map[string]any
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	// The turn is what keys this text to its bubble on rebuild; without it a
	// multi-turn run collapses its intermediate texts into one slot.
	if metadata["turn"] != float64(1) {
		t.Fatalf("turn metadata = %#v, want 1", metadata["turn"])
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
