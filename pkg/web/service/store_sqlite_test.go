package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
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

	if _, err := NewSQLiteStore(path); err == nil {
		t.Fatal("NewSQLiteStore() accepted an unversioned schema")
	}
}

func TestSQLiteStoreRejectsUnsupportedSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE chat_sessions (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			status TEXT NOT NULL,
			session_proto BLOB NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX idx_sessions_agent ON chat_sessions(agent_id);
		PRAGMA user_version = 2;
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	if _, err := NewSQLiteStore(path); err == nil {
		t.Fatal("NewSQLiteStore() accepted an unsupported schema version")
	}
}

func TestSQLiteStoreRejectsHistoricalSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "historical.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE historical_data (id TEXT PRIMARY KEY);
		PRAGMA user_version = 3;
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	_ = db.Close()
	if _, err := NewSQLiteStore(path); err == nil || !strings.Contains(err.Error(), "unsupported sqlite schema version") {
		t.Fatalf("NewSQLiteStore() error = %v, want unsupported historical schema", err)
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
	_ = types.SetWebMessage(user, &types.WebMessageMetadata{Code: "x"})
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "aop-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "s1")
	event := &aop.Event{
		Id: "event-retry", SessionId: "s1", Emitter: "aiscan",
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "missing-event-id.db"))
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scans.db"))
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
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "protojson.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	scan := &types.Scan{
		Id: "scan-json", Target: "example.com", Mode: "deep",
		Options: &types.ScanOptions{Verify: true, Sniper: true},
		Status:  types.ScanStatus_SCAN_STATUS_RUNNING, Progress: "enumerating",
		Report: "# report", Error: "", CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}

	var raw, target, mode, status, progress, report string
	var verify, sniper, deep bool
	if err := store.db.QueryRow(`
		SELECT scan_json, target, mode, verify, sniper, deep, status, progress, report
		FROM scans WHERE id = ?`, scan.Id,
	).Scan(&raw, &target, &mode, &verify, &sniper, &deep, &status, &progress, &report); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(raw)) {
		t.Fatalf("scan_json is not JSON: %q", raw)
	}
	if target != scan.Target || mode != scan.Mode || status != scanStatusToDB(scan.Status) || progress != scan.Progress || report != scan.Report {
		t.Fatalf("relational projection = target:%q mode:%q status:%q progress:%q report:%q", target, mode, status, progress, report)
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

func TestSQLiteStoreDoesNotDuplicateLargeReportInSnapshot(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "report-dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	report := strings.Repeat("report-line\n", 4<<20/12)
	scan := &types.Scan{
		Id: "scan-report-dedup", Target: "example.com", Mode: "quick", Report: report,
		Status: types.ScanStatus_SCAN_STATUS_COMPLETED, CreatedAt: nowProto(), UpdatedAt: nowProto(),
	}
	if err := store.Create(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	var snapshotBytes, reportBytes int
	var raw string
	if err := store.db.QueryRow(`SELECT scan_json, length(scan_json), length(report) FROM scans WHERE id = ?`, scan.Id).
		Scan(&raw, &snapshotBytes, &reportBytes); err != nil {
		t.Fatal(err)
	}
	if reportBytes != len(report) {
		t.Fatalf("report column bytes = %d, want %d", reportBytes, len(report))
	}
	if strings.Contains(raw, "report-line") || snapshotBytes >= len(report) {
		t.Fatalf("scan_json still duplicates the large report: snapshot_bytes=%d report_bytes=%d", snapshotBytes, reportBytes)
	}
	got, err := store.Get(context.Background(), scan.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Report != report {
		t.Fatalf("Get() report bytes = %d, want %d", len(got.Report), len(report))
	}
}

func TestSQLiteStoreKeepsSCOObservationForEveryOperation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sco.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	node := json.RawMessage(`{"cstx_id":"ip:127.0.0.1","cstx_type":"ip","ip":"127.0.0.1"}`)
	for _, operationID := range []string{"scan-1", "scan-2"} {
		if err := store.UpsertSCONodes(context.Background(), operationID, []json.RawMessage{node}); err != nil {
			t.Fatal(err)
		}
	}
	for _, operationID := range []string{"scan-1", "scan-2"} {
		nodes, err := store.ListSCONodesByScanID(context.Background(), operationID, "", 10)
		if err != nil || len(nodes) != 1 {
			t.Fatalf("operation %s nodes = %d, err = %v; want 1", operationID, len(nodes), err)
		}
	}
	var nodeCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sco_nodes`).Scan(&nodeCount); err != nil || nodeCount != 1 {
		t.Fatalf("global SCO node count = %d, err = %v; want 1", nodeCount, err)
	}
}

func TestSQLiteStoreTransitionScanRequiresExpectedStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "transitions.db"))
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
