package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

func TestSQLiteStoreWipesLegacyTextEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT, status TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE chat_aop_events (id TEXT PRIMARY KEY, session_id TEXT, event_json TEXT, created_at TEXT);
		INSERT INTO chat_sessions VALUES ('s1','','','','active','2026-07-19T00:00:00Z','2026-07-19T00:00:00Z');
		INSERT INTO chat_aop_events VALUES ('e1','s1','{"type":"text","ts":"2026-07-19T00:00:01Z","session_id":"s1","agent":"aiscan","data":"{}"}','2026-07-19T00:00:01Z');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events, err := store.ListAOPEvents(context.Background(), "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("legacy text events survived the wipe: %+v", events)
	}
}

func TestSQLiteStoreMessageRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	created := time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC)
	if err := store.AddMessage(ctx, &ChatMessage{
		ID: "m1", SessionID: "s1", Role: "user", Content: "hello",
		Metadata: json.RawMessage(`{"code":"x"}`), CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	assistant := aop.Event{
		Type:      aop.TypeMessage,
		TS:        created.Add(time.Second).Format(time.RFC3339Nano),
		SessionID: "s1",
		Agent:     "aiscan",
		Data: mustJSON(aop.MessageData{
			MessageID: "m-1", Role: "assistant",
			Parts: []aop.MessagePart{{Type: aop.PartText, Text: "hi there"}},
		}),
	}
	if err := store.AddAOPEvent(ctx, "s1", assistant); err != nil {
		t.Fatal(err)
	}
	// Deltas are streaming fragments and must never be persisted.
	delta := aop.Event{
		Type:      aop.TypeMessageDelta,
		TS:        created.Add(2 * time.Second).Format(time.RFC3339Nano),
		SessionID: "s1",
		Agent:     "aiscan",
		Data: mustJSON(aop.MessageDeltaData{
			MessageID: "m-1", PartIndex: 0, PartType: aop.PartText, Delta: "hi",
		}),
	}
	if err := store.AddAOPEvent(ctx, "s1", delta); err != nil {
		t.Fatal(err)
	}

	msgs, err := store.ListMessages(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v, want 2", msgs)
	}
	if msgs[0].ID != "m1" || msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("user message = %+v", msgs[0])
	}
	var meta map[string]any
	if err := json.Unmarshal(msgs[0].Metadata, &meta); err != nil || meta["code"] != "x" {
		t.Fatalf("user metadata = %s, err = %v", msgs[0].Metadata, err)
	}
	if msgs[1].ID != "m-1" || msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Fatalf("assistant message = %+v", msgs[1])
	}

	events, err := store.ListAOPEvents(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == aop.TypeMessageDelta {
			t.Fatalf("delta was persisted: %+v", e)
		}
	}
}

func TestSQLiteStorePersistsAnalysisOptions(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scans.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	job := &ScanJob{
		ID:        "scan-1",
		Target:    "127.0.0.1",
		Mode:      "quick",
		Verify:    true,
		Deep:      true,
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Verify || got.Sniper || !got.Deep {
		t.Fatalf("stored options = verify:%v sniper:%v deep:%v", got.Verify, got.Sniper, got.Deep)
	}
}
