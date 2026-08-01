package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	"github.com/chainreactors/aiscan/core/output"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestListSessionPageDoesNotDeadlockOnNonEmptyStore(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session-page.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session-1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sessions, more, err := store.ListSessionPage(ctx, 0, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(sessions) != 1 || sessions[0].ID != "session-1" {
		t.Fatalf("ListSessionPage = %+v more=%v", sessions, more)
	}
}

func TestSQLiteStoreIgnoresNonProtoJSONEventsWithoutDeletingHistory(t *testing.T) {
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
		t.Fatalf("non-protobuf JSON event was decoded: %+v", events)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM chat_aop_events WHERE session_id = 's1'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("stored history rows = %d, err=%v; want preserved row", rows, err)
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
	rows, err := store.db.Query(`SELECT cursor, id FROM chat_aop_events WHERE session_id = 's1' ORDER BY cursor`)
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
			t.Fatalf("cursor for %s = %d, want %d", id, seq, len(got))
		}
	}
	if len(got) != 2 || got[0] != "e1" || got[1] != "e2" {
		t.Fatalf("backfilled order = %v, want [e1 e2]", got)
	}
	cursor, persisted, err := store.AppendAOPEvent(context.Background(), "s1", &aop.Event{
		Id: "e3", EmittedAt: timestamppb.New(time.Date(2026, 7, 19, 0, 0, 3, 0, time.UTC)), SessionId: "s1", Emitter: "aiscan",
		Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}},
	})
	if err != nil || !persisted || cursor != 3 {
		t.Fatalf("AppendAOPEvent cursor = %d, persisted = %v, err = %v; want 3, true, nil", cursor, persisted, err)
	}
}

func TestSQLiteStoreAOPMessageRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	createStoredSession(t, store, "s1")

	created := time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC)
	user := &aop.Event{
		Id: "e-user", EmittedAt: timestamppb.New(created), SessionId: "s1", Emitter: "operator",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "m1", Role: "user", Content: []*aop.Content{aop.Text("hello")}}},
	}
	_ = ext.SetWebMessage(user, ext.WebMessageExtension{Metadata: []byte(`{"code":"x"}`)})
	if err := store.AddAOPEvent(ctx, "s1", user); err != nil {
		t.Fatal(err)
	}
	assistant := &aop.Event{
		Id: "e-message", EmittedAt: timestamppb.New(created.Add(time.Second)), SessionId: "s1", Emitter: "aiscan",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("hi there")},
		}},
	}
	if err := store.AddAOPEvent(ctx, "s1", assistant); err != nil {
		t.Fatal(err)
	}
	// Deltas are streaming fragments and must never be persisted.
	delta := &aop.Event{
		Id: "e-delta", EmittedAt: timestamppb.New(created.Add(2 * time.Second)), SessionId: "s1", Emitter: "aiscan",
		Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
			MessageId: "m-1", ContentIndex: 0, Value: &aop.MessageDelta_Text{Text: "hi"},
		}},
	}
	if err := store.AddAOPEvent(ctx, "s1", delta); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListAOPEvents(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2", events)
	}
	if message := events[0].GetMessage(); message.GetId() != "m1" || message.GetRole() != "user" || message.GetContent()[0].GetText().GetText() != "hello" {
		t.Fatalf("user event = %+v", events[0])
	}
	var meta map[string]any
	webExtension, ok, err := ext.GetWebMessage(events[0])
	if err != nil || !ok {
		t.Fatalf("web extension = %+v, ok = %v, err = %v", webExtension, ok, err)
	}
	if err := json.Unmarshal(webExtension.Metadata, &meta); err != nil || meta["code"] != "x" {
		t.Fatalf("user metadata = %s, err = %v", webExtension.Metadata, err)
	}
	if message := events[1].GetMessage(); message.GetId() != "m-1" || message.GetRole() != "assistant" || message.GetContent()[0].GetText().GetText() != "hi there" {
		t.Fatalf("assistant event = %+v", events[1])
	}
	for _, e := range events {
		if e.GetMessageDelta() != nil {
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
	if err := store.AddAOPEvent(ctx, session.ID, &aop.Event{
		Id: "event-cascade", EmittedAt: timestamppb.New(now), SessionId: session.ID, Emitter: "operator",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-cascade", Role: "user", Content: []*aop.Content{aop.Text("hello")}}},
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

func TestSQLiteStoreRejectsAOPEventForMissingSession(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "foreign-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.AddAOPEvent(context.Background(), "missing", &aop.Event{
		Id: "orphan-event", EmittedAt: timestamppb.Now(), SessionId: "missing", Emitter: "operator",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "orphan-message", Role: "user", Content: []*aop.Content{aop.Text("hello")}}},
	})
	if err == nil {
		t.Fatal("AddAOPEvent() created an orphan event")
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
