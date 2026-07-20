package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/aop"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS scans (
			id         TEXT PRIMARY KEY,
			target     TEXT NOT NULL,
			mode       TEXT NOT NULL DEFAULT 'quick',
			ai         INTEGER NOT NULL DEFAULT 0,
			verify     INTEGER NOT NULL DEFAULT 0,
			sniper     INTEGER NOT NULL DEFAULT 0,
			deep       INTEGER NOT NULL DEFAULT 0,
			status     TEXT NOT NULL DEFAULT 'queued',
			progress   TEXT NOT NULL DEFAULT '',
			report     TEXT NOT NULL DEFAULT '',
			result     TEXT NOT NULL DEFAULT '',
			error      TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS chat_sessions (
			id         TEXT PRIMARY KEY,
			agent_id   TEXT NOT NULL DEFAULT '',
			agent_name TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS chat_aop_events (
			id         TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
			event_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS session_scans (
			session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
			scan_id    TEXT NOT NULL,
			PRIMARY KEY (session_id, scan_id)
		);
	`); err != nil {
		return err
	}

	for _, column := range []sqliteColumnMigration{
		{table: "scans", name: "mode", definition: "TEXT NOT NULL DEFAULT 'quick'"},
		{table: "scans", name: "ai", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "scans", name: "verify", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "scans", name: "sniper", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "scans", name: "deep", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "scans", name: "progress", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "scans", name: "report", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "scans", name: "result", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "scans", name: "error", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_sessions", name: "agent_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_sessions", name: "agent_name", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_sessions", name: "title", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_sessions", name: "status", definition: "TEXT NOT NULL DEFAULT 'active'"},
		{table: "chat_sessions", name: "topic_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_messages", name: "agent_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_messages", name: "agent_name", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_messages", name: "metadata", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureSQLiteColumn(db, column); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS records (
			id         TEXT PRIMARY KEY,
			type       TEXT NOT NULL,
			scan_id    TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			agent_id   TEXT NOT NULL DEFAULT '',
			source     TEXT NOT NULL DEFAULT '',
			target     TEXT NOT NULL DEFAULT '',
			turn       INTEGER NOT NULL DEFAULT 0,
			priority   TEXT NOT NULL DEFAULT '',
			summary    TEXT NOT NULL DEFAULT '',
			loot       INTEGER NOT NULL DEFAULT 0,
			tags       TEXT NOT NULL DEFAULT '',
			data       TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
	`); err != nil {
		return err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sco_nodes (
			cstx_id    TEXT PRIMARY KEY,
			cstx_type  TEXT NOT NULL,
			data       TEXT NOT NULL,
			scan_id    TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`); err != nil {
		return err
	}

	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_scans_created ON scans(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sessions_updated ON chat_sessions(updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sessions_agent ON chat_sessions(agent_id);
		CREATE INDEX IF NOT EXISTS idx_aop_events_session ON chat_aop_events(session_id, created_at, id);
		CREATE INDEX IF NOT EXISTS idx_sco_nodes_type ON sco_nodes(cstx_type);
		CREATE INDEX IF NOT EXISTS idx_sco_nodes_scan ON sco_nodes(scan_id);
	`); err != nil {
		return err
	}
	return migrateLegacyChatMessages(db)
}

type sqliteColumnMigration struct {
	table      string
	name       string
	definition string
}

func ensureSQLiteColumn(db *sql.DB, column sqliteColumnMigration) error {
	tableExists, err := sqliteTableExists(db, column.table)
	if err != nil || !tableExists {
		return err
	}
	exists, err := sqliteColumnExists(db, column.table, column.name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN %s %s",
		quoteSQLiteIdent(column.table),
		quoteSQLiteIdent(column.name),
		column.definition,
	))
	return err
}

func sqliteTableExists(db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func migrateLegacyChatMessages(db *sql.DB) error {
	exists, err := sqliteTableExists(db, "chat_messages")
	if err != nil || !exists {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existingAssistantText := map[string]map[string]bool{}
	aopRows, err := tx.Query(`SELECT session_id, event_json FROM chat_aop_events`)
	if err != nil {
		return err
	}
	for aopRows.Next() {
		var sessionID, raw string
		if err := aopRows.Scan(&sessionID, &raw); err != nil {
			aopRows.Close()
			return err
		}
		var event aop.Event
		var data aop.TextData
		if json.Unmarshal([]byte(raw), &event) != nil || event.Type != aop.TypeText ||
			json.Unmarshal(event.Data, &data) != nil || data.Delta ||
			data.Channel == aop.TextChannelReasoning || data.Role == "user" {
			continue
		}
		if existingAssistantText[sessionID] == nil {
			existingAssistantText[sessionID] = map[string]bool{}
		}
		existingAssistantText[sessionID][data.Content] = true
	}
	if err := aopRows.Close(); err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT id, session_id, role, agent_id, agent_name, content, metadata, created_at FROM chat_messages ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	var messages []*ChatMessage
	for rows.Next() {
		var msg ChatMessage
		var metadata, createdAt string
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.AgentID, &msg.AgentName, &msg.Content, &metadata, &createdAt); err != nil {
			rows.Close()
			return err
		}
		if metadata != "" {
			msg.Metadata = json.RawMessage(metadata)
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		messages = append(messages, &msg)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, msg := range messages {
		// Older versions dual-wrote an aggregate assistant message beside the
		// final AOP text. Skip only that exact duplicate; unrelated AOP rows do
		// not prove the legacy message was persisted elsewhere.
		if msg.Role == "assistant" && existingAssistantText[msg.SessionID][msg.Content] {
			continue
		}
		event, err := aopEventFromChatMessage(msg)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO chat_aop_events (id, session_id, event_json, created_at) VALUES (?, ?, ?, ?)`,
			"legacy:"+msg.ID, msg.SessionID, string(raw), event.TS,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE chat_messages`); err != nil {
		return err
	}
	return tx.Commit()
}

func aopEventFromChatMessage(msg *ChatMessage) (aop.Event, error) {
	if msg == nil || msg.SessionID == "" {
		return aop.Event{}, fmt.Errorf("chat message requires session_id")
	}
	createdAt := msg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	agentName := strings.TrimSpace(msg.AgentName)
	if agentName == "" {
		agentName = "aiscan.web"
	}
	data, err := json.Marshal(aop.TextData{Content: msg.Content, Role: msg.Role})
	if err != nil {
		return aop.Event{}, err
	}
	metadata := any(nil)
	if len(msg.Metadata) > 0 {
		if err := json.Unmarshal(msg.Metadata, &metadata); err != nil {
			metadata = string(msg.Metadata)
		}
	}
	ext := map[string]any{"message_id": msg.ID}
	if msg.AgentID != "" {
		ext["agent_id"] = msg.AgentID
	}
	if metadata != nil {
		ext["metadata"] = metadata
	}
	return aop.Event{
		Type:      aop.TypeText,
		TS:        createdAt.UTC().Format(time.RFC3339Nano),
		SessionID: msg.SessionID,
		Agent:     agentName,
		Data:      data,
		Ext:       map[string]any{"aiscan": ext},
	}, nil
}

func aopExtension(event aop.Event, namespace string) map[string]any {
	if event.Ext == nil {
		return nil
	}
	ext, _ := event.Ext[namespace].(map[string]any)
	return ext
}

func sqliteColumnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdent(table)))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func quoteSQLiteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) Create(ctx context.Context, job *ScanJob) error {
	resultJSON := marshalResult(job)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scans (id, target, mode, ai, verify, sniper, deep, status, progress, report, result, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Target, job.Mode, boolToInt(job.Verify || job.Sniper), boolToInt(job.Verify), boolToInt(job.Sniper), boolToInt(job.Deep),
		string(job.Status), job.Progress, job.Report, resultJSON, job.Error,
		job.CreatedAt.Format(time.RFC3339Nano), job.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*ScanJob, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target, mode, ai, verify, sniper, deep, status, progress, report, result, error, created_at, updated_at
		 FROM scans WHERE id = ?`, id)
	return scanRow(row)
}

func (s *SQLiteStore) List(ctx context.Context, limit int) ([]*ScanJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target, mode, ai, verify, sniper, deep, status, progress, report, result, error, created_at, updated_at
		 FROM scans ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*ScanJob
	for rows.Next() {
		job, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, job *ScanJob) error {
	resultJSON := marshalResult(job)
	_, err := s.db.ExecContext(ctx,
		`UPDATE scans SET ai=?, verify=?, sniper=?, deep=?, status=?, progress=?, report=?, result=?, error=?, updated_at=? WHERE id=?`,
		boolToInt(job.Verify || job.Sniper), boolToInt(job.Verify), boolToInt(job.Sniper), boolToInt(job.Deep),
		string(job.Status), job.Progress, job.Report, resultJSON, job.Error,
		job.UpdatedAt.Format(time.RFC3339Nano), job.ID,
	)
	return err
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scans WHERE id=?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFromScanner(sc scanner) (*ScanJob, error) {
	var job ScanJob
	var status, resultJSON, createdAt, updatedAt string
	var ai, verify, sniper, deep int
	err := sc.Scan(&job.ID, &job.Target, &job.Mode, &ai, &verify, &sniper, &deep, &status,
		&job.Progress, &job.Report, &resultJSON, &job.Error, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	_ = ai
	job.Verify = verify != 0
	job.Sniper = sniper != 0
	job.Deep = deep != 0
	job.Status = ScanStatus(status)
	if resultJSON != "" {
		_ = json.Unmarshal([]byte(resultJSON), &job.Result)
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &job, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func marshalResult(job *ScanJob) string {
	if job == nil || job.Result == nil {
		return ""
	}
	data, err := json.Marshal(job.Result)
	if err != nil {
		return ""
	}
	return string(data)
}

func scanRow(row *sql.Row) (*ScanJob, error) {
	return scanFromScanner(row)
}

func scanRows(rows *sql.Rows) (*ScanJob, error) {
	return scanFromScanner(rows)
}

// --- Chat session CRUD ---

func (s *SQLiteStore) CreateSession(ctx context.Context, session *ChatSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (id, agent_id, agent_name, title, status, topic_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.AgentID, session.AgentName, session.Title, session.Status, session.TopicID,
		session.CreatedAt.Format(time.RFC3339Nano), session.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*ChatSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, agent_name, title, status, topic_id, created_at, updated_at FROM chat_sessions WHERE id = ?`, id)
	var cs ChatSession
	var createdAt, updatedAt string
	if err := row.Scan(&cs.ID, &cs.AgentID, &cs.AgentName, &cs.Title, &cs.Status, &cs.TopicID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	cs.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	cs.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	scanIDs, _ := s.SessionScanIDs(ctx, id)
	cs.ScanIDs = scanIDs
	return &cs, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, limit int) ([]*ChatSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, agent_name, title, status, topic_id, created_at, updated_at FROM chat_sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*ChatSession
	for rows.Next() {
		var cs ChatSession
		var createdAt, updatedAt string
		if err := rows.Scan(&cs.ID, &cs.AgentID, &cs.AgentName, &cs.Title, &cs.Status, &cs.TopicID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		cs.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		cs.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		sessions = append(sessions, &cs)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) UpdateSession(ctx context.Context, session *ChatSession) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chat_sessions SET title=?, status=?, topic_id=?, updated_at=? WHERE id=?`,
		session.Title, session.Status, session.TopicID, session.UpdatedAt.Format(time.RFC3339Nano), session.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id=?`, id)
	return err
}

// --- Chat message CRUD ---

func (s *SQLiteStore) AddMessage(ctx context.Context, msg *ChatMessage) error {
	event, err := aopEventFromChatMessage(msg)
	if err != nil {
		return err
	}
	return s.AddAOPEvent(ctx, msg.SessionID, event)
}

// ClearMessages deletes every message in a session without removing the session
// itself — the store half of web /clear ("clear conversation"). Messages are leaf
// rows (nothing references them), so a single delete suffices.
func (s *SQLiteStore) ClearMessages(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_aop_events WHERE session_id = ?`, sessionID)
	return err
}

func (s *SQLiteStore) AddAOPEvent(ctx context.Context, sessionID string, event aop.Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	createdAt := event.TS
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO chat_aop_events (id, session_id, event_json, created_at) VALUES (?, ?, ?, ?)`,
		generateID(), sessionID, string(raw), createdAt,
	)
	return err
}

func (s *SQLiteStore) ListAOPEvents(ctx context.Context, sessionID string, limit int) ([]aop.Event, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT event_json FROM chat_aop_events WHERE session_id = ? ORDER BY created_at ASC, rowid ASC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]aop.Event, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event aop.Event
		if json.Unmarshal([]byte(raw), &event) == nil && event.Valid() {
			events = append(events, event)
		}
	}
	return events, rows.Err()
}

func (s *SQLiteStore) ListMessages(ctx context.Context, sessionID string, limit int) ([]*ChatMessage, error) {
	if limit <= 0 {
		limit = 500
	}
	events, err := s.ListAOPEvents(ctx, sessionID, 10000)
	if err != nil {
		return nil, err
	}
	capacity := len(events)
	if capacity > limit {
		capacity = limit
	}
	msgs := make([]*ChatMessage, 0, capacity)
	for index, event := range events {
		if event.Type != aop.TypeText {
			continue
		}
		var data aop.TextData
		if json.Unmarshal(event.Data, &data) != nil || data.Delta || data.Channel == aop.TextChannelReasoning {
			continue
		}
		msg := &ChatMessage{
			ID:        fmt.Sprintf("aop:%s:%d", sessionID, index),
			SessionID: sessionID,
			Role:      data.Role,
			AgentName: event.Agent,
			Content:   data.Content,
		}
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339Nano, event.TS)
		if ext := aopExtension(event, "aiscan"); ext != nil {
			if value, ok := ext["message_id"].(string); ok && value != "" {
				msg.ID = value
			}
			msg.AgentID, _ = ext["agent_id"].(string)
			if metadata, ok := ext["metadata"]; ok {
				msg.Metadata, _ = json.Marshal(metadata)
			}
		}
		msgs = append(msgs, msg)
		if len(msgs) >= limit {
			break
		}
	}
	return msgs, nil
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

// --- Records ---

func (s *SQLiteStore) InsertRecord(ctx context.Context, rec *output.Record) error {
	return s.InsertRecords(ctx, []*output.Record{rec})
}

func (s *SQLiteStore) InsertRecords(ctx context.Context, recs []*output.Record) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO records (id, type, scan_id, session_id, agent_id, source, target, turn, priority, summary, loot, tags, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, rec := range recs {
		tagsJSON, _ := json.Marshal(rec.Tags)
		if _, err := stmt.ExecContext(ctx,
			rec.ID, string(rec.Type), rec.ScanID, rec.SessionID, rec.AgentID,
			rec.Source, rec.Target, rec.Turn, rec.Priority, rec.Summary,
			boolToInt(rec.Loot), string(tagsJSON), string(rec.Data),
			rec.Timestamp.Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ── SCO Nodes ──

func (s *SQLiteStore) UpsertSCONodes(ctx context.Context, scanID string, nodes []json.RawMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO sco_nodes (cstx_id, cstx_type, data, scan_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, COALESCE((SELECT created_at FROM sco_nodes WHERE cstx_id = ?), ?), ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	now := time.Now().Format(time.RFC3339Nano)
	for _, raw := range nodes {
		var header struct {
			Type string `json:"cstx_type"`
			ID   string `json:"cstx_id"`
		}
		if json.Unmarshal(raw, &header) != nil || header.ID == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			header.ID, header.Type, string(raw), scanID, header.ID, now, now,
		); err != nil {
			_ = tx.Rollback()
			return err
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
	if scanID != "" {
		where = append(where, "scan_id = ?")
		args = append(args, scanID)
	}
	if nodeType != "" {
		where = append(where, "cstx_type = ?")
		args = append(args, nodeType)
	}
	var qb strings.Builder
	qb.WriteString("SELECT data FROM sco_nodes")
	if len(where) > 0 {
		qb.WriteString(" WHERE ")
		qb.WriteString(strings.Join(where, " AND "))
	}
	qb.WriteString(" ORDER BY updated_at DESC LIMIT ?")
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM sco_nodes WHERE scan_id = ?`, scanID)
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
