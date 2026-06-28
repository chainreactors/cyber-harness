package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

		CREATE TABLE IF NOT EXISTS chat_messages (
			id         TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
			role       TEXT NOT NULL,
			agent_id   TEXT NOT NULL DEFAULT '',
			agent_name TEXT NOT NULL DEFAULT '',
			content    TEXT NOT NULL DEFAULT '',
			metadata   TEXT NOT NULL DEFAULT '',
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
		{table: "chat_messages", name: "agent_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_messages", name: "agent_name", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "chat_messages", name: "metadata", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureSQLiteColumn(db, column); err != nil {
			return err
		}
	}

	_, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_scans_created ON scans(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sessions_updated ON chat_sessions(updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sessions_agent ON chat_sessions(agent_id);
		CREATE INDEX IF NOT EXISTS idx_messages_session ON chat_messages(session_id, created_at);
	`)
	return err
}

type sqliteColumnMigration struct {
	table      string
	name       string
	definition string
}

func ensureSQLiteColumn(db *sql.DB, column sqliteColumnMigration) error {
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
	normalizeJobAnalysis(job)
	resultJSON := marshalResult(job)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scans (id, target, mode, ai, verify, sniper, deep, status, progress, report, result, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Target, job.Mode, boolToInt(job.AI), boolToInt(job.Verify), boolToInt(job.Sniper), boolToInt(job.Deep),
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
	normalizeJobAnalysis(job)
	resultJSON := marshalResult(job)
	_, err := s.db.ExecContext(ctx,
		`UPDATE scans SET ai=?, verify=?, sniper=?, deep=?, status=?, progress=?, report=?, result=?, error=?, updated_at=? WHERE id=?`,
		boolToInt(job.AI), boolToInt(job.Verify), boolToInt(job.Sniper), boolToInt(job.Deep),
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
	job.AI = ai != 0
	job.Verify = verify != 0
	job.Sniper = sniper != 0
	job.Deep = deep != 0
	normalizeJobAnalysis(&job)
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

func normalizeJobAnalysis(job *ScanJob) {
	if job == nil {
		return
	}
	if job.AI && !job.Verify && !job.Sniper {
		job.Verify = true
		job.Sniper = true
	}
	job.AI = job.Verify || job.Sniper
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
		`INSERT INTO chat_sessions (id, agent_id, agent_name, title, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.AgentID, session.AgentName, session.Title, session.Status,
		session.CreatedAt.Format(time.RFC3339Nano), session.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*ChatSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, agent_name, title, status, created_at, updated_at FROM chat_sessions WHERE id = ?`, id)
	var cs ChatSession
	var createdAt, updatedAt string
	if err := row.Scan(&cs.ID, &cs.AgentID, &cs.AgentName, &cs.Title, &cs.Status, &createdAt, &updatedAt); err != nil {
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
		`SELECT id, agent_id, agent_name, title, status, created_at, updated_at FROM chat_sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*ChatSession
	for rows.Next() {
		var cs ChatSession
		var createdAt, updatedAt string
		if err := rows.Scan(&cs.ID, &cs.AgentID, &cs.AgentName, &cs.Title, &cs.Status, &createdAt, &updatedAt); err != nil {
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
		`UPDATE chat_sessions SET title=?, status=?, updated_at=? WHERE id=?`,
		session.Title, session.Status, session.UpdatedAt.Format(time.RFC3339Nano), session.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id=?`, id)
	return err
}

// --- Chat message CRUD ---

func (s *SQLiteStore) AddMessage(ctx context.Context, msg *ChatMessage) error {
	metadata := ""
	if msg.Metadata != nil {
		metadata = string(msg.Metadata)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_messages (id, session_id, role, agent_id, agent_name, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.SessionID, msg.Role, msg.AgentID, msg.AgentName, msg.Content, metadata,
		msg.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ListMessages(ctx context.Context, sessionID string, limit int) ([]*ChatMessage, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, agent_id, agent_name, content, metadata, created_at
		 FROM chat_messages WHERE session_id = ? ORDER BY created_at ASC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []*ChatMessage
	for rows.Next() {
		var m ChatMessage
		var metadata, createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.AgentID, &m.AgentName, &m.Content, &metadata, &createdAt); err != nil {
			return nil, err
		}
		if metadata != "" {
			m.Metadata = json.RawMessage(metadata)
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		msgs = append(msgs, &m)
	}
	return msgs, rows.Err()
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
