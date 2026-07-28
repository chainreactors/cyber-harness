package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/aop"
	"github.com/chainreactors/aiscan/core/output"
)

func createStoredSession(t *testing.T, store *SQLiteStore, id string) {
	t.Helper()
	now := time.Now()
	if err := store.CreateSession(context.Background(), &ChatSession{
		ID: id, Status: SessionActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession(%q): %v", id, err)
	}
}

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

func TestSQLiteStoreBackfillsDurableEventSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-sequence.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT, status TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE chat_aop_events (id TEXT PRIMARY KEY, session_id TEXT, event_json TEXT, created_at TEXT);
		INSERT INTO chat_sessions VALUES ('s1','','','','active','2026-07-19T00:00:00Z','2026-07-19T00:00:00Z');
		INSERT INTO chat_aop_events VALUES
			('e2','s1','{"type":"status","ts":"2026-07-19T00:00:02Z","session_id":"s1","agent":"aiscan","data":{}}','2026-07-19T00:00:02Z'),
			('e1','s1','{"type":"status","ts":"2026-07-19T00:00:01Z","session_id":"s1","agent":"aiscan","data":{}}','2026-07-19T00:00:01Z');
		PRAGMA user_version = 2;
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
	rows, err := store.db.Query(`SELECT hub_seq, id FROM chat_aop_events WHERE session_id = 's1' ORDER BY hub_seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var seq int
		var id string
		if err := rows.Scan(&seq, &id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
		if seq != len(got) {
			t.Fatalf("hub_seq for %s = %d, want %d", id, seq, len(got))
		}
	}
	if len(got) != 2 || got[0] != "e1" || got[1] != "e2" {
		t.Fatalf("backfilled order = %v, want [e1 e2]", got)
	}
	cursor, persisted, err := store.AppendAOPEvent(context.Background(), "s1", aop.Event{
		Type: aop.TypeStatus, TS: "2026-07-19T00:00:03Z", SessionID: "s1", Agent: "aiscan", Data: json.RawMessage(`{}`),
	})
	if err != nil || !persisted || cursor != 3 {
		t.Fatalf("AppendAOPEvent cursor = %d, persisted = %v, err = %v; want 3, true, nil", cursor, persisted, err)
	}
}

func TestSQLiteStoreMessageRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	createStoredSession(t, store, "s1")

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

func TestSQLiteStoreMessagePaginationIgnoresNonMessageDensity(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "message-pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	createStoredSession(t, store, "s1")

	for message := 1; message <= 4; message++ {
		for event := 0; event < 25; event++ {
			if err := store.AddAOPEvent(ctx, "s1", aop.Event{
				Type: aop.TypeStatus, TS: time.Now().UTC().Format(time.RFC3339Nano), SessionID: "s1", Agent: "aiscan", Data: json.RawMessage(`{}`),
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.AddMessage(ctx, &ChatMessage{
			ID: string(rune('0' + message)), SessionID: "s1", Role: "user", Content: "message", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := store.ListMessagePage(ctx, "s1", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Items) != 2 || latest.Items[0].ID != "3" || latest.Items[1].ID != "4" || latest.NextCursor == 0 {
		t.Fatalf("latest page = %+v", latest)
	}
	older, err := store.ListMessagePage(ctx, "s1", latest.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Items) != 2 || older.Items[0].ID != "1" || older.Items[1].ID != "2" || older.NextCursor != 0 {
		t.Fatalf("older page = %+v", older)
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

func TestSQLiteStoreTransitionScanRequiresExpectedStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "transitions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	job := &ScanJob{
		ID: "scan-transition", Target: "127.0.0.1", Mode: "quick",
		Status: StatusQueued, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	job.Status = StatusCanceled
	job.UpdatedAt = time.Now()
	changed, err := store.TransitionScan(context.Background(), job, StatusQueued, StatusRunning)
	if err != nil || !changed {
		t.Fatalf("queued -> canceled = %v, %v; want true, nil", changed, err)
	}

	job.Status = StatusCompleted
	job.Result = &output.Result{}
	changed, err = store.TransitionScan(context.Background(), job, StatusRunning)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("terminal canceled status was overwritten")
	}
	stored, err := store.Get(context.Background(), job.ID)
	if err != nil || stored.Status != StatusCanceled {
		t.Fatalf("stored scan = %+v, %v", stored, err)
	}
}

func TestSQLiteStoreEnablesForeignKeysAndCascadesSessionData(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "foreign-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var enabled int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", enabled)
	}

	ctx := context.Background()
	now := time.Now()
	session := &ChatSession{
		ID: "session-cascade", Status: SessionActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(ctx, &ChatMessage{
		ID: "message-cascade", SessionID: session.ID, Role: "user", Content: "hello", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkScanToSession(ctx, session.ID, "scan-cascade"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"chat_aop_events", "session_scans"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id = ?`, session.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after session deletion", table, count)
		}
	}
}

func TestSQLiteStoreRejectsMessageForMissingSession(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "foreign-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.AddMessage(context.Background(), &ChatMessage{
		ID: "orphan-message", SessionID: "missing", Role: "user", Content: "hello", CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("AddMessage() created an orphan event")
	}
}

func TestSQLiteStoreMigratesLegacySessionForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-foreign-keys.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (
			id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT,
			status TEXT, created_at TEXT, updated_at TEXT
		);
		CREATE TABLE chat_aop_events (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			event_json TEXT NOT NULL, created_at TEXT NOT NULL
		);
		CREATE TABLE session_scans (
			session_id TEXT NOT NULL, scan_id TEXT NOT NULL,
			PRIMARY KEY (session_id, scan_id)
		);
		INSERT INTO chat_sessions VALUES (
			'kept-session','','','','active','2026-07-27T00:00:00Z','2026-07-27T00:00:00Z'
		);
		INSERT INTO chat_aop_events VALUES
			('kept-event','kept-session','{}','2026-07-27T00:00:01Z'),
			('orphan-event','missing-session','{}','2026-07-27T00:00:02Z');
		INSERT INTO session_scans VALUES
			('kept-session','kept-scan'),
			('missing-session','orphan-scan');
		PRAGMA user_version = 2;
	`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, table := range []string{"chat_aop_events", "session_scans"} {
		var keptCount int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM ` + table + ` WHERE session_id = 'kept-session'`,
		).Scan(&keptCount); err != nil {
			t.Fatal(err)
		}
		if keptCount != 1 {
			t.Fatalf("%s retained %d valid legacy rows, want 1", table, keptCount)
		}
		var orphanCount int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM ` + table + ` WHERE session_id = 'missing-session'`,
		).Scan(&orphanCount); err != nil {
			t.Fatal(err)
		}
		if orphanCount != 0 {
			t.Fatalf("%s retained %d legacy orphan rows", table, orphanCount)
		}
	}

	if err := store.DeleteSession(context.Background(), "kept-session"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"chat_aop_events", "session_scans"} {
		var count int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM ` + table + ` WHERE session_id = 'kept-session'`,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s did not cascade after legacy migration", table)
		}
	}
}
