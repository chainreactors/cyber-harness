package service

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestIncompatibleEventSchemaIsRejectedWithoutModification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incompatible.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`ALTER TABLE chat_aop_events RENAME COLUMN event_proto TO event_json`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := NewSQLiteStore(path); err == nil || !strings.Contains(err.Error(), "chat_aop_events columns") {
		t.Fatalf("incompatible server open = %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`SELECT event_json FROM chat_aop_events`); err != nil {
		t.Fatalf("server changed the rejected schema: %v", err)
	}
}

func TestStoredEventsRetainUnknownProtobufData(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "session")
	event := &aop.Event{Id: "unknown-extension", SessionId: "session", Seq: 1,
		Payload: &aop.Event_Extension{Extension: &anypb.Any{TypeUrl: "type.example/future.Event", Value: []byte{8, 42}}}}
	event.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, _, err := store.AppendAOPEvent(t.Context(), "session", event); err != nil {
		t.Fatal(err)
	}
	page, _, err := store.ListAOPEventsAfter(t.Context(), "session", 0, 10)
	if err != nil || len(page) != 1 || !proto.Equal(page[0].Event, event) {
		t.Fatalf("unknown payload was changed: %v, %v", page, err)
	}
	var kind string
	if err := store.db.QueryRow(`SELECT typeof(event_proto) FROM chat_aop_events`).Scan(&kind); err != nil || kind != "blob" {
		t.Fatalf("event storage = %q, %v", kind, err)
	}
}
