package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func createStoredSession(t *testing.T, store *SQLiteStore, id string) {
	t.Helper()
	if err := store.CreateSession(context.Background(), &types.SessionRecord{
		Session: &aop.Session{Id: id, State: SessionStateOpen}, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatalf("CreateSession(%q): %v", id, err)
	}
}

func TestListSessionPageDoesNotDeadlockOnNonEmptyStore(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session-page.db"), ScanSchema)
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
	if more || len(sessions) != 1 || sessions[0].GetSession().GetId() != "session-1" {
		t.Fatalf("ListSessionPage = %+v more=%v", sessions, more)
	}
}

func TestSQLiteStoreRejectsUnversionedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unversioned.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE chat_sessions (id TEXT PRIMARY KEY, agent_id TEXT, agent_name TEXT, title TEXT, status TEXT, created_at TEXT, updated_at TEXT);
		INSERT INTO chat_sessions VALUES ('s1','','','','active','2026-07-19T00:00:00Z','2026-07-19T00:00:00Z');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	if _, err := NewSQLiteStore(path, ScanSchema); err == nil {
		t.Fatal("NewSQLiteStore() accepted an unversioned schema")
	}
}

// A host without a scan console opens the core schema only: four tables, and
// session reads skip the scan backfill instead of touching absent tables.
func TestSQLiteStoreCoreSchemaOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core-only.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tables, err := schemaTables(store.db)
	if err != nil {
		t.Fatal(err)
	}
	if want := coreSchema.tableNames(); !slices.Equal(tables, want) {
		t.Fatalf("core-only tables = %v, want %v", tables, want)
	}
	createStoredSession(t, store, "session-1")
	session, err := store.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.ScanIds) != 0 {
		t.Fatalf("core-only store backfilled scan ids: %v", session.ScanIds)
	}
	if _, _, err := store.ListSessionPage(context.Background(), 0, 100, true); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
}

// A database holding module tables is rejected when the module is not
// configured, and vice versa: the union must match exactly.
func TestSQLiteStoreModuleUnionIsExact(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "full.db")
	store, err := NewSQLiteStore(full, ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := NewSQLiteStore(full); err == nil {
		t.Fatal("scan database accepted without the scan module")
	}
	bare := filepath.Join(root, "bare.db")
	store, err = NewSQLiteStore(bare)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := NewSQLiteStore(bare, ScanSchema); err == nil {
		t.Fatal("core database accepted with the scan module")
	}
}

func TestSQLiteStoreAOPMessageRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"), ScanSchema)
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
	_ = types.SetWebMessage(user, &types.WebMessageMetadata{Code: "x"})
	if err := store.AddAOPEvent(ctx, "s1", user); err != nil {
		t.Fatal(err)
	}
	assistant := &aop.Event{
		Id: "e-message", EmittedAt: timestamppb.New(created.Add(time.Second)), SessionId: "s1", Emitter: "cyber",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("hi there")},
		}},
	}
	if err := store.AddAOPEvent(ctx, "s1", assistant); err != nil {
		t.Fatal(err)
	}
	// Deltas are streaming fragments and must never be persisted.
	delta := &aop.Event{
		Id: "e-delta", EmittedAt: timestamppb.New(created.Add(2 * time.Second)), SessionId: "s1", Emitter: "cyber",
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
	webExtension, ok, err := types.GetWebMessage(events[0])
	if err != nil || !ok {
		t.Fatalf("web extension = %+v, ok = %v, err = %v", webExtension, ok, err)
	}
	if webExtension.GetCode() != "x" {
		t.Fatalf("user metadata = %+v", webExtension)
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

func TestSQLiteStoreAppendAOPEventIsIdempotentByEventID(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "aop-idempotency.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "s1")
	event := &aop.Event{
		Id: "event-retry", SessionId: "s1", Emitter: "cyber",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("once")}}},
	}
	firstCursor, firstPersisted, err := store.AppendAOPEvent(context.Background(), "s1", event)
	if err != nil || !firstPersisted || firstCursor != 1 {
		t.Fatalf("first append = cursor:%d persisted:%v err:%v", firstCursor, firstPersisted, err)
	}
	var storedEventID string
	if err := store.db.QueryRow(`SELECT event_id FROM chat_aop_events WHERE session_id = ?`, "s1").Scan(&storedEventID); err != nil {
		t.Fatal(err)
	}
	if storedEventID != event.Id {
		t.Fatalf("stored event id = %q, want %q", storedEventID, event.Id)
	}
	secondCursor, secondPersisted, err := store.AppendAOPEvent(context.Background(), "s1", proto.Clone(event).(*aop.Event))
	if err != nil || secondPersisted || secondCursor != firstCursor {
		t.Fatalf("retry append = cursor:%d persisted:%v err:%v", secondCursor, secondPersisted, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM chat_aop_events WHERE session_id = ?`, "s1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted event count = %d, want 1", count)
	}
}

func TestSQLiteStoreRejectsAOPEventWithoutIdentity(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "missing-event-id.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, err := store.AppendAOPEvent(context.Background(), "s1", &aop.Event{
		SessionId: "s1", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}},
	}); err == nil {
		t.Fatal("AppendAOPEvent accepted an event without an id")
	}
}

func TestSQLiteStorePersistsAnalysisOptions(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scans.db"), ScanSchema)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	scan := &types.Scan{
		Id:        "scan-1",
		Target:    "127.0.0.1",
		Mode:      "quick",
		Options:   &types.ScanOptions{Verify: true, Deep: true},
		Status:    types.ScanStatus_SCAN_STATUS_QUEUED,
		CreatedAt: nowProto(),
		UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(context.Background(), scan.Id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	options := got.GetOptions()
	if !options.GetVerify() || options.GetSniper() || !options.GetDeep() {
		t.Fatalf("stored options = verify:%v sniper:%v deep:%v", options.GetVerify(), options.GetSniper(), options.GetDeep())
	}
}

func TestSQLiteStoreUsesProtoJSONAndRelationalScanColumns(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "protojson.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	scan := &types.Scan{
		Id: "scan-json", Target: "example.com", Mode: "deep",
		Options: &types.ScanOptions{Verify: true, Sniper: true},
		Status:  types.ScanStatus_SCAN_STATUS_RUNNING, Progress: "enumerating",
		Error: "", CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}

	var raw, target, mode, status, progress string
	var verify, sniper, deep bool
	if err := store.db.QueryRow(`
		SELECT scan_json, target, mode, verify, sniper, deep, status, progress
		FROM scans WHERE id = ?`, scan.Id,
	).Scan(&raw, &target, &mode, &verify, &sniper, &deep, &status, &progress); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(raw)) {
		t.Fatalf("scan_json is not JSON: %q", raw)
	}
	if target != scan.Target || mode != scan.Mode || status != scanStatusToDB(scan.Status) || progress != scan.Progress {
		t.Fatalf("relational projection = target:%q mode:%q status:%q progress:%q", target, mode, status, progress)
	}
	if !verify || !sniper || deep {
		t.Fatalf("relational options = verify:%v sniper:%v deep:%v", verify, sniper, deep)
	}
	var obsoleteColumns int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('scans') WHERE name = 'scan_proto'`).Scan(&obsoleteColumns); err != nil {
		t.Fatal(err)
	}
	if obsoleteColumns != 0 {
		t.Fatal("obsolete scan_proto BLOB column still exists")
	}
}

func TestSQLiteStoreArtifactArchiveRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "artifacts.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := testArtifactEvent(t, "artifact-1")
	second := testArtifactEvent(t, "artifact-2")
	deliveries, err := store.SyncArtifactEvents(context.Background(), []*aop.Event{first, first, second}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 || deliveries[0].GetCursor() != "1" {
		t.Fatalf("artifact deliveries = %+v", deliveries)
	}
	secondCursor, err := strconv.ParseInt(deliveries[1].GetCursor(), 10, 64)
	if err != nil || secondCursor <= 1 {
		t.Fatalf("second artifact cursor = %q, %v", deliveries[1].GetCursor(), err)
	}
	if !proto.Equal(deliveries[0].GetEvent(), first) || !proto.Equal(deliveries[1].GetEvent(), second) {
		t.Fatal("artifact archive did not preserve the original events")
	}
	next, err := store.SyncArtifactEvents(context.Background(), nil, 1)
	if err != nil || len(next) != 1 || next[0].GetEvent().GetId() != second.GetId() {
		t.Fatalf("artifact archive resume = %+v, %v", next, err)
	}
	var rawCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM raw_artifacts`).Scan(&rawCount); err != nil || rawCount != 2 {
		t.Fatalf("retained raw artifacts = %d, %v; want 2", rawCount, err)
	}
}

func TestSQLiteStoreArtifactArchiveUsesFixedPages(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "artifact-pages.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events := make([]*aop.Event, artifactSyncBatch+1)
	for index := range events {
		events[index] = testArtifactEvent(t, fmt.Sprintf("artifact-%03d", index))
	}
	first, err := store.SyncArtifactEvents(context.Background(), events, 0)
	if err != nil || len(first) != artifactSyncBatch {
		t.Fatalf("first artifact page = %d, %v", len(first), err)
	}
	second, err := store.SyncArtifactEvents(context.Background(), nil, artifactSyncBatch)
	if err != nil || len(second) != 1 {
		t.Fatalf("second artifact page = %d, %v", len(second), err)
	}
}

func testArtifactEvent(t *testing.T, id string) *aop.Event {
	t.Helper()
	payload, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Data: []byte(`{"ip":"127.0.0.1"}`), MediaType: aop.JSONMediaType})
	if err != nil {
		t.Fatal(err)
	}
	return &aop.Event{Id: id, EmittedAt: timestamppb.Now(), Payload: &aop.Event_Extension{Extension: payload}}
}

func TestSQLiteStoreTransitionScanRequiresExpectedStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "transitions.db"), ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	scan := &types.Scan{
		Id: "scan-transition", Target: "127.0.0.1", Mode: "quick",
		Status: types.ScanStatus_SCAN_STATUS_QUEUED, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}

	scan.Status = types.ScanStatus_SCAN_STATUS_CANCELED
	scan.UpdatedAt = nowProto()
	changed, err := store.TransitionScan(context.Background(), scan, types.ScanStatus_SCAN_STATUS_QUEUED, types.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		t.Fatalf("queued -> canceled = %v, %v; want true, nil", changed, err)
	}

	scan.Status = types.ScanStatus_SCAN_STATUS_COMPLETED
	changed, err = store.TransitionScan(context.Background(), scan, types.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("terminal canceled status was overwritten")
	}
	stored, err := store.Get(context.Background(), scan.Id)
	if err != nil || stored.Status != types.ScanStatus_SCAN_STATUS_CANCELED {
		t.Fatalf("stored scan = %+v, %v", stored, err)
	}
}

func TestSQLiteStoreEnablesForeignKeysAndCascadesSessionData(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "foreign-keys.db"), ScanSchema)
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
	session := &types.SessionRecord{
		Session:   &aop.Session{Id: "session-cascade", State: SessionStateOpen},
		CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAOPEvent(ctx, session.GetSession().GetId(), &aop.Event{
		Id: "event-cascade", EmittedAt: timestamppb.New(now), SessionId: session.GetSession().GetId(), Emitter: "operator",
		Payload: &aop.Event_Message{Message: &aop.Message{Id: "message-cascade", Role: "user", Content: []*aop.Content{aop.Text("hello")}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, &types.Scan{
		Id: "scan-cascade", Target: "127.0.0.1", Mode: "quick",
		Status: types.ScanStatus_SCAN_STATUS_COMPLETED, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkScanToSession(ctx, session.GetSession().GetId(), "scan-cascade"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, session.GetSession().GetId()); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"chat_aop_events", "session_scans"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id = ?`, session.GetSession().GetId()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after session deletion", table, count)
		}
	}
}

func TestSQLiteStoreRejectsAOPEventForMissingSession(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "foreign-keys.db"), ScanSchema)
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
