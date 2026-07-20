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

func TestSQLiteStoreMigratesLegacyMessagesIntoAOPAndDropsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT, status TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE chat_messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, agent_id TEXT, agent_name TEXT, content TEXT, metadata TEXT, created_at TEXT);
		INSERT INTO chat_sessions VALUES ('s1','','','','active','2026-07-19T00:00:00Z','2026-07-19T00:00:00Z');
		INSERT INTO chat_messages VALUES ('m1','s1','user','','','hello','{}','2026-07-19T00:00:01Z');
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
	msgs, err := store.ListMessages(context.Background(), "s1", 10)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("migrated messages = %+v, err = %v", msgs, err)
	}
	exists, err := sqliteTableExists(store.db, "chat_messages")
	if err != nil || exists {
		t.Fatalf("legacy table exists = %v, err = %v", exists, err)
	}
}

func TestSQLiteStoreMigrationOnlySkipsExactAssistantDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-with-aop.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT, status TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE chat_messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, agent_id TEXT, agent_name TEXT, content TEXT, metadata TEXT, created_at TEXT);
		CREATE TABLE chat_aop_events (id TEXT PRIMARY KEY, session_id TEXT, event_json TEXT, created_at TEXT);
		INSERT INTO chat_sessions VALUES ('s1','','','','active','2026-07-19T00:00:00Z','2026-07-19T00:00:00Z');
		INSERT INTO chat_messages VALUES ('m1','s1','assistant','','aiscan','keep me','{}','2026-07-19T00:00:01Z');
		INSERT INTO chat_messages VALUES ('m2','s1','assistant','','aiscan','already stored','{}','2026-07-19T00:00:02Z');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	data, _ := json.Marshal(aop.TextData{Content: "already stored", Role: "assistant"})
	event, _ := json.Marshal(aop.Event{
		Type: aop.TypeText, TS: "2026-07-19T00:00:02Z", SessionID: "s1",
		Agent: "aiscan", Data: data,
	})
	if _, err := db.Exec(`INSERT INTO chat_aop_events VALUES ('e1','s1',?,'2026-07-19T00:00:02Z')`, string(event)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msgs, err := store.ListMessages(context.Background(), "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Content != "keep me" || msgs[1].Content != "already stored" {
		t.Fatalf("migrated messages = %+v", msgs)
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
