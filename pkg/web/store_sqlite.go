package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

const sqliteSchemaVersion = 2

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		db.Close()
		if err != nil {
			return nil, fmt.Errorf("verify sqlite foreign keys: %w", err)
		}
		return nil, fmt.Errorf("verify sqlite foreign keys: disabled")
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == sqliteSchemaVersion {
		return nil
	}
	if version == 1 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`
			DROP INDEX idx_sessions_agent;
			ALTER TABLE chat_sessions RENAME COLUMN agent_id TO node_id;
			CREATE INDEX idx_sessions_node_id ON chat_sessions(node_id);
			PRAGMA user_version = 2;
		`); err != nil {
			return err
		}
		return tx.Commit()
	}
	if version != 0 {
		return fmt.Errorf("unsupported sqlite schema version %d; delete the database and restart", version)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return err
	}
	if tables != 0 {
		return fmt.Errorf("legacy sqlite schema is not supported; delete the database and restart")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		CREATE TABLE scans (
			id         TEXT PRIMARY KEY,
			target     TEXT NOT NULL,
			status     TEXT NOT NULL,
			scan_proto BLOB NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE chat_sessions (
			id            TEXT PRIMARY KEY,
			node_id       TEXT NOT NULL,
			status        TEXT NOT NULL,
			session_proto BLOB NOT NULL,
			created_at    TEXT NOT NULL,
			updated_at    TEXT NOT NULL
		);

		CREATE TABLE chat_aop_events (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
			cursor      INTEGER NOT NULL,
			event_proto BLOB NOT NULL,
			created_at  TEXT NOT NULL,
			UNIQUE (session_id, cursor)
		);

		CREATE TABLE session_scans (
			session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
			scan_id    TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
			PRIMARY KEY (session_id, scan_id)
		);

		CREATE TABLE aop_request_journal (
			request_id     TEXT PRIMARY KEY,
			method         TEXT NOT NULL,
			request_hash   BLOB NOT NULL,
			response_proto BLOB NOT NULL,
			created_at     TEXT NOT NULL
		);

		CREATE TABLE sco_nodes (
			cstx_id    TEXT PRIMARY KEY,
			cstx_type  TEXT NOT NULL,
			data       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE sco_observations (
			operation_id TEXT NOT NULL,
			cstx_id      TEXT NOT NULL REFERENCES sco_nodes(cstx_id) ON DELETE CASCADE,
			observed_at  TEXT NOT NULL,
			PRIMARY KEY (operation_id, cstx_id)
		);

		CREATE INDEX idx_scans_created ON scans(created_at DESC);
		CREATE INDEX idx_sessions_updated ON chat_sessions(updated_at DESC);
		CREATE INDEX idx_sessions_node_id ON chat_sessions(node_id);
		CREATE INDEX idx_aop_events_session ON chat_aop_events(session_id, cursor);
		CREATE INDEX idx_sco_nodes_type ON sco_nodes(cstx_type);
		CREATE INDEX idx_sco_observations_node ON sco_observations(cstx_id);
		PRAGMA user_version = 2;
	`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) LoadAOPRequest(ctx context.Context, requestID, method string, requestHash []byte, response protobuf.Message) (found, conflict bool, err error) {
	if s == nil || strings.TrimSpace(requestID) == "" || response == nil {
		return false, false, nil
	}
	var storedMethod string
	var storedHash []byte
	var raw []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT method, request_hash, response_proto FROM aop_request_journal WHERE request_id = ?`, requestID,
	).Scan(&storedMethod, &storedHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if storedMethod != method || !bytes.Equal(storedHash, requestHash) {
		return false, true, nil
	}
	if err := protobuf.Unmarshal(raw, response); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *SQLiteStore) SaveAOPRequest(ctx context.Context, requestID, method string, requestHash []byte, response protobuf.Message) error {
	if s == nil || strings.TrimSpace(requestID) == "" || response == nil {
		return nil
	}
	raw, err := protobuf.Marshal(response)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO aop_request_journal (request_id, method, request_hash, response_proto, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, requestID, method, requestHash, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ── Scans ──
//
// scan_proto is the canonical payload. Flat columns exist only for filtering
// and ordering; they never reconstruct the protobuf message.

func (s *SQLiteStore) Create(ctx context.Context, scan *types.Scan) error {
	raw, err := protobuf.Marshal(scan)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO scans (id, target, status, scan_proto, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		scan.Id, scan.Target, scanStatusToDB(scan.Status), raw,
		formatProtoTime(scan.CreatedAt), formatProtoTime(scan.UpdatedAt),
	)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*types.Scan, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT scan_proto
		 FROM scans WHERE id = ?`, id)
	return scanRow(row)
}

func (s *SQLiteStore) List(ctx context.Context, limit int) ([]*types.Scan, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT scan_proto
		 FROM scans ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scans []*types.Scan
	for rows.Next() {
		scan, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, scan *types.Scan) error {
	raw, err := protobuf.Marshal(scan)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE scans SET status=?, scan_proto=?, updated_at=? WHERE id=?`,
		scanStatusToDB(scan.Status), raw,
		formatProtoTime(scan.UpdatedAt), scan.Id,
	)
	return err
}

// TransitionScan updates a scan only while it is in one of the expected
// states. Callers use the affected-row result to make terminal states
// immutable when cancellation and completion race.
func (s *SQLiteStore) TransitionScan(ctx context.Context, scan *types.Scan, expected ...types.ScanStatus) (bool, error) {
	if scan == nil {
		return false, fmt.Errorf("scan is required")
	}
	if len(expected) == 0 {
		return false, fmt.Errorf("at least one expected scan status is required")
	}

	raw, err := protobuf.Marshal(scan)
	if err != nil {
		return false, err
	}
	placeholders := make([]string, len(expected))
	args := []any{
		scanStatusToDB(scan.Status), raw,
		formatProtoTime(scan.UpdatedAt), scan.Id,
	}
	for i, status := range expected {
		placeholders[i] = "?"
		args = append(args, scanStatusToDB(status))
	}
	//nolint:gosec // only fixed "?" placeholders are concatenated; statuses remain bound arguments
	query := `UPDATE scans SET status=?, scan_proto=?, updated_at=?
		 WHERE id=? AND status IN (` + strings.Join(placeholders, ",") + `)`
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scans WHERE id=?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFromScanner(sc scanner) (*types.Scan, error) {
	var raw []byte
	if err := sc.Scan(&raw); err != nil {
		return nil, err
	}
	scan := new(types.Scan)
	if err := protobuf.Unmarshal(raw, scan); err != nil {
		return nil, fmt.Errorf("decode scan protobuf: %w", err)
	}
	return scan, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatProtoTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}

func scanRow(row *sql.Row) (*types.Scan, error) {
	return scanFromScanner(row)
}

func scanRows(rows *sql.Rows) (*types.Scan, error) {
	return scanFromScanner(rows)
}

// --- Chat session CRUD ---
//
// session_proto is the canonical payload. Flat columns exist only for
// filtering and ordering.

func (s *SQLiteStore) CreateSession(ctx context.Context, session *types.SessionRecord) error {
	raw, err := protobuf.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (id, node_id, status, session_proto, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		session.GetSession().GetId(), session.GetSession().GetNodeId(), session.GetSession().GetState(),
		raw,
		formatProtoTime(session.CreatedAt), formatProtoTime(session.UpdatedAt),
	)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*types.SessionRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT session_proto FROM chat_sessions WHERE id = ?`, id)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	session := new(types.SessionRecord)
	if err := protobuf.Unmarshal(raw, session); err != nil {
		return nil, fmt.Errorf("decode session protobuf: %w", err)
	}
	session.ScanIds, _ = s.SessionScanIDs(ctx, id)
	return session, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, limit int) ([]*types.SessionRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_proto FROM chat_sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*types.SessionRecord
	for rows.Next() {
		session, err := sessionFromRow(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) ListSessionPage(ctx context.Context, offset, limit int, includeClosed bool) ([]*types.SessionRecord, bool, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT session_proto
		FROM chat_sessions ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args := []any{limit + 1, offset}
	if !includeClosed {
		query = `SELECT session_proto
			FROM chat_sessions WHERE status = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?`
		args = []any{SessionStateOpen, limit + 1, offset}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	sessions := make([]*types.SessionRecord, 0, limit+1)
	for rows.Next() {
		session, err := sessionFromRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	// SQLiteStore intentionally uses one connection. Enrich only after closing
	// the session row set; querying SessionScanIDs inside rows.Next would wait on
	// the connection held by the outer query and deadlock every non-empty page.
	for _, session := range sessions {
		scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
		session.ScanIds = scanIDs
	}
	hasMore := len(sessions) > limit
	if hasMore {
		sessions = sessions[:limit]
	}
	return sessions, hasMore, nil
}

func sessionFromRow(rows *sql.Rows) (*types.SessionRecord, error) {
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return nil, err
	}
	session := new(types.SessionRecord)
	if err := protobuf.Unmarshal(raw, session); err != nil {
		return nil, fmt.Errorf("decode session protobuf: %w", err)
	}
	return session, nil
}

func (s *SQLiteStore) UpdateSession(ctx context.Context, session *types.SessionRecord) error {
	scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
	if len(scanIDs) > 0 {
		session.ScanIds = scanIDs
	}
	raw, err := protobuf.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE chat_sessions SET status=?, session_proto=?, updated_at=? WHERE id=?`,
		session.GetSession().GetState(), raw,
		formatProtoTime(session.UpdatedAt), session.GetSession().GetId(),
	)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id=?`, id)
	return err
}

func (s *SQLiteStore) AddAOPEvent(ctx context.Context, sessionID string, event *aop.Event) error {
	_, _, err := s.AppendAOPEvent(ctx, sessionID, event)
	return err
}

// AppendAOPEvent persists one durable event and assigns the authoritative
// session-local cursor used by ListEvents/WatchEvents replay. Message deltas
// remain transient and return persisted=false.
func (s *SQLiteStore) AppendAOPEvent(ctx context.Context, sessionID string, event *aop.Event) (cursor int64, persisted bool, err error) {
	// Deltas are streaming fragments; only complete messages are persisted so a
	// replayed history holds the authoritative state.
	if event == nil || event.GetMessageDelta() != nil || event.GetToolCallDelta() != nil {
		return 0, false, nil
	}
	raw, err := protobuf.Marshal(event)
	if err != nil {
		return 0, false, err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if event.EmittedAt != nil {
		createdAt = event.EmittedAt.AsTime().UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(cursor), 0) + 1 FROM chat_aop_events WHERE session_id = ?`, sessionID,
	).Scan(&cursor); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO chat_aop_events (id, session_id, cursor, event_proto, created_at) VALUES (?, ?, ?, ?, ?)`,
		generateID(), sessionID, cursor, raw, createdAt,
	); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return cursor, true, nil
}

func (s *SQLiteStore) ListAOPEvents(ctx context.Context, sessionID string, limit int) ([]*aop.Event, error) {
	page, _, err := s.ListAOPEventPage(ctx, sessionID, 0, limit)
	if err != nil {
		return nil, err
	}
	events := make([]*aop.Event, 0, len(page))
	for _, stored := range page {
		events = append(events, stored.Event)
	}
	return events, nil
}

func (s *SQLiteStore) MaxAOPEventSeq(ctx context.Context, sessionID string) (uint64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_proto FROM chat_aop_events WHERE session_id = ?`, sessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var maximum uint64
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		event := new(aop.Event)
		if protobuf.Unmarshal(raw, event) == nil && event.Seq > maximum {
			maximum = event.Seq
		}
	}
	return maximum, rows.Err()
}

func (s *SQLiteStore) ListAOPEventPage(ctx context.Context, sessionID string, before int64, limit int) ([]persistedAOPEvent, int64, error) {
	if limit <= 0 {
		limit = 10000
	}
	if limit > 10000 {
		limit = 10000
	}
	query := `SELECT cursor, event_proto FROM (
		SELECT cursor, event_proto FROM chat_aop_events
		WHERE session_id = ? ORDER BY cursor DESC LIMIT ?
	) ORDER BY cursor ASC`
	args := []any{sessionID, limit + 1}
	if before > 0 {
		query = `SELECT cursor, event_proto FROM (
			SELECT cursor, event_proto FROM chat_aop_events
			WHERE session_id = ? AND cursor < ? ORDER BY cursor DESC LIMIT ?
		) ORDER BY cursor ASC`
		args = []any{sessionID, before, limit + 1}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := make([]persistedAOPEvent, 0, limit+1)
	for rows.Next() {
		var raw []byte
		var cursor int64
		if err := rows.Scan(&cursor, &raw); err != nil {
			return nil, 0, err
		}
		event := new(aop.Event)
		if protobuf.Unmarshal(raw, event) == nil && event.SessionId != "" && event.Payload != nil {
			events = append(events, persistedAOPEvent{Cursor: cursor, Event: event})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(events) > limit {
		events = events[1:]
		if len(events) > 0 {
			next = events[0].Cursor
		}
	}
	return events, next, nil
}

func (s *SQLiteStore) ListAOPEventsAfter(ctx context.Context, sessionID string, after int64, limit int) ([]persistedAOPEvent, error) {
	if after <= 0 {
		events, _, err := s.ListAOPEventPage(ctx, sessionID, 0, limit)
		return events, err
	}
	query := `SELECT cursor, event_proto FROM chat_aop_events WHERE session_id = ? AND cursor > ? ORDER BY cursor ASC`
	args := []any{sessionID, after}
	if limit > 0 {
		if limit > 10000 {
			limit = 10000
		}
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []persistedAOPEvent
	for rows.Next() {
		var stored persistedAOPEvent
		var raw []byte
		if err := rows.Scan(&stored.Cursor, &raw); err != nil {
			return nil, err
		}
		stored.Event = new(aop.Event)
		if protobuf.Unmarshal(raw, stored.Event) == nil && stored.Event.SessionId != "" && stored.Event.Payload != nil {
			events = append(events, stored)
		}
	}
	return events, rows.Err()
}

// --- Session-scan association ---

func (s *SQLiteStore) LinkScanToSession(ctx context.Context, sessionID, scanID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO session_scans (session_id, scan_id) VALUES (?, ?)`,
		sessionID, scanID,
	)
	return err
}

func (s *SQLiteStore) SessionScanIDs(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT scan_id FROM session_scans WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── SCO Nodes ──

func (s *SQLiteStore) UpsertSCONodes(ctx context.Context, operationID string, nodes []json.RawMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	nodeStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO sco_nodes (cstx_id, cstx_type, data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(cstx_id) DO UPDATE SET
			cstx_type = excluded.cstx_type,
			data = excluded.data,
			updated_at = excluded.updated_at`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer nodeStmt.Close()
	observationStmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO sco_observations (operation_id, cstx_id, observed_at) VALUES (?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer observationStmt.Close()
	now := time.Now().Format(time.RFC3339Nano)
	for _, raw := range nodes {
		var header struct {
			Type string `json:"cstx_type"`
			ID   string `json:"cstx_id"`
		}
		if json.Unmarshal(raw, &header) != nil || header.ID == "" {
			continue
		}
		if _, err := nodeStmt.ExecContext(ctx, header.ID, header.Type, string(raw), now, now); err != nil {
			_ = tx.Rollback()
			return err
		}
		if operationID != "" {
			if _, err := observationStmt.ExecContext(ctx, operationID, header.ID, now); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListSCONodes(ctx context.Context, nodeType string, limit int) ([]json.RawMessage, error) {
	return s.ListSCONodesByScanID(ctx, "", nodeType, limit)
}

func (s *SQLiteStore) ListSCONodesByScanID(ctx context.Context, scanID, nodeType string, limit int) ([]json.RawMessage, error) {
	var where []string
	var args []any
	from := "sco_nodes AS nodes"
	if scanID != "" {
		from += " JOIN sco_observations AS observations ON observations.cstx_id = nodes.cstx_id"
		where = append(where, "observations.operation_id = ?")
		args = append(args, scanID)
	}
	if nodeType != "" {
		where = append(where, "nodes.cstx_type = ?")
		args = append(args, nodeType)
	}
	var qb strings.Builder
	qb.WriteString("SELECT nodes.data FROM ")
	qb.WriteString(from)
	if len(where) > 0 {
		qb.WriteString(" WHERE ")
		qb.WriteString(strings.Join(where, " AND "))
	}
	qb.WriteString(" ORDER BY nodes.updated_at DESC LIMIT ?")
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, qb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []json.RawMessage
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		nodes = append(nodes, json.RawMessage(data))
	}
	return nodes, rows.Err()
}

func (s *SQLiteStore) GetSCONode(ctx context.Context, cstxID string) (json.RawMessage, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM sco_nodes WHERE cstx_id = ?`, cstxID).Scan(&data)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (s *SQLiteStore) DeleteSCONodesByScan(ctx context.Context, scanID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sco_observations WHERE operation_id = ?`, scanID)
	return err
}

func (s *SQLiteStore) SCONodeStats(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cstx_type, COUNT(*) FROM sco_nodes GROUP BY cstx_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := make(map[string]int)
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		stats[t] = c
	}
	return stats, rows.Err()
}
