package inbox

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	mailbox "github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// The persistence envelope deliberately separates protobuf from transport
// metadata. encoding/json cannot restore protobuf oneofs.
type storedMessage struct {
	Message     []byte
	Origin      mailbox.Origin
	Priority    mailbox.Priority
	Interrupt   bool
	Attachments []mailbox.Attachment
	Meta        mailbox.Metadata
	CreatedAt   time.Time
}

func encodeMessage(m mailbox.Message) ([]byte, error) {
	data, err := proto.Marshal(m.Message)
	if err != nil {
		return nil, err
	}
	return json.Marshal(storedMessage{data, m.Origin, m.Priority, m.Interrupt, m.Attachments, m.Meta, m.CreatedAt})
}
func decodeMessage(data []byte) (mailbox.Message, error) {
	var v storedMessage
	if err := json.Unmarshal(data, &v); err != nil {
		return mailbox.Message{}, err
	}
	m := new(aop.Message)
	if err := proto.Unmarshal(v.Message, m); err != nil {
		return mailbox.Message{}, err
	}
	return mailbox.Message{Message: m, Origin: v.Origin, Priority: v.Priority, Interrupt: v.Interrupt, Attachments: v.Attachments, Meta: v.Meta, CreatedAt: v.CreatedAt}, nil
}
func encodeHistory(messages []*aop.Message) ([]byte, error) {
	values := make([][]byte, len(messages))
	for i, m := range messages {
		b, err := proto.Marshal(m)
		if err != nil {
			return nil, err
		}
		values[i] = b
	}
	return json.Marshal(values)
}
func decodeHistory(data []byte) ([]*aop.Message, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var values [][]byte
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	messages := make([]*aop.Message, len(values))
	for i, b := range values {
		m := new(aop.Message)
		if err := proto.Unmarshal(b, m); err != nil {
			return nil, err
		}
		messages[i] = m
	}
	return messages, nil
}
func messageKey(key [4]string) string { b, _ := json.Marshal(key); return string(b) }

type activeStore struct {
	db    *sql.DB
	owner *sql.DB
	lock  *sql.Conn
}

func openActiveStore(dir string) (_ *activeStore, err error) {
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &activeStore{}
	defer func() {
		if err != nil {
			_ = s.close()
		}
	}()
	// A separate SQLite lock file provides an OS-released cross-platform
	// ownership lock, including after a hard crash. No stale PID deletion.
	s.owner, err = sql.Open("sqlite", filepath.Join(dir, "owner.sqlite"))
	if err != nil {
		return nil, err
	}
	s.owner.SetMaxOpenConns(1)
	s.lock, err = s.owner.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	if _, err = s.lock.ExecContext(context.Background(), "PRAGMA busy_timeout=0; BEGIN EXCLUSIVE"); err != nil {
		return nil, fmt.Errorf("active state directory is already owned: %w", err)
	}
	s.db, err = sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 0 && version != 2 {
		return nil, fmt.Errorf("unsupported active state schema %d: Harness protocol requires schema 2; use a new state directory", version)
	}
	_, err = s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS messages(seq INTEGER PRIMARY KEY AUTOINCREMENT,key TEXT UNIQUE NOT NULL,id TEXT NOT NULL,body BLOB NOT NULL,state TEXT NOT NULL DEFAULT 'pending',targets TEXT NOT NULL DEFAULT '[]',error TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS routes(message_key TEXT NOT NULL,target TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending',PRIMARY KEY(message_key,target));
 CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,name TEXT NOT NULL,label TEXT NOT NULL,state TEXT NOT NULL,history BLOB,source BLOB NOT NULL,session_id TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS inputs(seq INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL,message_key TEXT NOT NULL,body BLOB NOT NULL,state TEXT NOT NULL DEFAULT 'pending',session_id TEXT NOT NULL DEFAULT '',UNIQUE(task_id,message_key));
 CREATE TABLE IF NOT EXISTS attempts(session_id TEXT PRIMARY KEY,task_id TEXT NOT NULL,state TEXT NOT NULL,proc_id TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS outbox(id TEXT PRIMARY KEY,body BLOB NOT NULL,state TEXT NOT NULL DEFAULT 'pending');
 CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS retries(message_key TEXT PRIMARY KEY,attempts INTEGER NOT NULL,due INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS execution_events(session_id TEXT NOT NULL,seq INTEGER NOT NULL,event BLOB NOT NULL,PRIMARY KEY(session_id,seq));
 PRAGMA user_version=2;
 UPDATE messages SET state='pending' WHERE state='routing';
 UPDATE tasks SET state='interrupted' WHERE state='running';
 UPDATE inputs SET state='pending' WHERE state='running';
 UPDATE attempts SET state='interrupted' WHERE state='running';`)
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *activeStore) close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.lock != nil {
		_, _ = s.lock.ExecContext(context.Background(), "ROLLBACK")
		err = errors.Join(err, s.lock.Close())
	}
	if s.owner != nil {
		err = errors.Join(err, s.owner.Close())
	}
	return err
}

func (s *activeStore) admit(r *Receipt, capacity int) (*Receipt, error) {
	key := messageKey(r.key)
	existing, err := s.receipt(key)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM messages WHERE state IN ('pending','routing')").Scan(&count); err != nil {
		return nil, err
	}
	if count >= capacity {
		return nil, mailbox.ErrInboxFull
	}
	body, err := encodeMessage(r.message)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec("INSERT INTO messages(key,id,body) VALUES(?,?,?)", key, r.ID, body)
	return r, err
}
func (s *activeStore) receipt(key string) (*Receipt, error) {
	var id, state, targets, problem string
	var body []byte
	err := s.db.QueryRow("SELECT id,body,state,targets,error FROM messages WHERE key=?", key).Scan(&id, &body, &state, &targets, &problem)
	if err != nil {
		return nil, err
	}
	msg, err := decodeMessage(body)
	if err != nil {
		return nil, err
	}
	r := &Receipt{ID: id, message: msg, done: make(chan struct{})}
	if err = json.Unmarshal([]byte(key), &r.key); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(targets), &r.targets); err != nil {
		return nil, err
	}
	if state == "done" {
		if problem != "" {
			r.err = errors.New(problem)
		}
		r.once.Do(func() { close(r.done) })
	}
	return r, nil
}
func (s *activeStore) next() (*Receipt, error) {
	var key string
	if err := s.db.QueryRow("SELECT key FROM messages LEFT JOIN retries ON retries.message_key=messages.key WHERE state='pending' AND COALESCE(due,0)<=? ORDER BY seq LIMIT 1", time.Now().UnixMilli()).Scan(&key); err != nil {
		return nil, err
	}
	r, err := s.receipt(key)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec("UPDATE messages SET state='routing' WHERE key=?", key)
	return r, err
}
func (s *activeStore) routeDone(r *Receipt, targets []string, history []*aop.Message, problem error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	state, detail := "done", ""
	if problem != nil {
		state, detail = "pending", problem.Error()
		var attempts int
		if err = tx.QueryRow("SELECT COALESCE((SELECT attempts FROM retries WHERE message_key=?),0)", messageKey(r.key)).Scan(&attempts); err != nil {
			return err
		}
		delay := time.Second << min(attempts, 6)
		if _, err = tx.Exec("INSERT OR REPLACE INTO retries(message_key,attempts,due) VALUES(?,?,?)", messageKey(r.key), attempts+1, time.Now().Add(delay).UnixMilli()); err != nil {
			return err
		}
	} else if _, err = tx.Exec("DELETE FROM retries WHERE message_key=?", messageKey(r.key)); err != nil {
		return err
	}
	b, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE messages SET state=?,targets=?,error=? WHERE key=?", state, string(b), detail, messageKey(r.key)); err != nil {
		return err
	}
	b, err = encodeHistory(history)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT OR REPLACE INTO kv(key,value) VALUES('router_history',?)", b); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *activeStore) hasRoute(key, target string) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM routes WHERE message_key=? AND target=? AND state='done'", key, target).Scan(&n)
	return n != 0, err
}
func (s *activeStore) markRoute(key, target string) error {
	_, err := s.db.Exec("INSERT INTO routes(message_key,target,state) VALUES(?,?,'done') ON CONFLICT(message_key,target) DO UPDATE SET state='done'", key, target)
	return err
}
func (s *activeStore) planRoute(key, target string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO routes(message_key,target) VALUES(?,?)", key, target)
	return err
}
func (s *activeStore) routeTargets(key string) ([]string, error) {
	rows, err := s.db.Query("SELECT target FROM routes WHERE message_key=? ORDER BY rowid", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

type taskRecord struct {
	ID        string `json:"task_id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	State     string `json:"state"`
	SessionID string `json:"session_id,omitempty"`
}

func (s *activeStore) tasks() ([]taskRecord, error) {
	rows, err := s.db.Query(`SELECT id,name,label,state,session_id FROM tasks
 ORDER BY (SELECT MIN(seq) FROM inputs WHERE task_id=tasks.id AND state='pending'), tasks.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []taskRecord
	for rows.Next() {
		var t taskRecord
		if err = rows.Scan(&t.ID, &t.Name, &t.Label, &t.State, &t.SessionID); err != nil {
			return nil, err
		}
		values = append(values, t)
	}
	return values, rows.Err()
}
func (s *activeStore) enqueue(r *Receipt, id, name, label string) (string, error) {
	key := messageKey(r.key)
	creating := id == ""
	if id == "" {
		sum := sha256.Sum256([]byte(key))
		id = "task-" + hex.EncodeToString(sum[:12])
	}
	body, err := encodeMessage(r.message)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if creating {
		if _, err = tx.Exec("INSERT OR IGNORE INTO tasks(id,name,label,state,source) VALUES(?,?,?,'queued',?)", id, name, label, body); err != nil {
			return "", err
		}
	}
	var state string
	if err = tx.QueryRow("SELECT state FROM tasks WHERE id=?", id).Scan(&state); err != nil {
		return "", err
	}
	if state == "canceled" {
		return "", fmt.Errorf("task %s was canceled", id)
	}
	var exists int
	if err = tx.QueryRow("SELECT count(*) FROM inputs WHERE task_id=? AND message_key=?", id, key).Scan(&exists); err != nil {
		return "", err
	}
	if exists != 0 {
		return id, nil
	}
	// Transport IDs are scoped by origin/sender/session. Give the execution
	// transcript that same identity so equal IDs from different sources cannot
	// suppress each other on resume. Keep the source ID in the envelope metadata.
	input := cloneMessage(r.message)
	inputID := sha256.Sum256([]byte(key))
	input.Message.Id = "input-" + hex.EncodeToString(inputID[:])
	if input.Meta.MessageID == "" {
		input.Meta.MessageID = r.ID
	}
	body, err = encodeMessage(input)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO inputs(task_id,message_key,body) VALUES(?,?,?)", id, key, body); err != nil {
		return "", err
	}
	if _, err = tx.Exec("UPDATE tasks SET state='queued' WHERE id=? AND state NOT IN ('running','interrupted')", id); err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT OR REPLACE INTO routes(message_key,target,state) VALUES(?,?,'done')", key, "task:"+id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

type taskInput struct {
	seq     int64
	message mailbox.Message
}

func (s *activeStore) claim(t taskRecord, sessionID string, maxInputs int) ([]taskInput, []*aop.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var data []byte
	var previous string
	if err = tx.QueryRow("SELECT history,session_id FROM tasks WHERE id=?", t.ID).Scan(&data, &previous); err != nil {
		return nil, nil, err
	}
	history, err := decodeHistory(data)
	if err != nil {
		return nil, nil, err
	}
	// Only events since the last committed snapshot are journaled. A complete
	// checkpoint deletes them in the same transaction as saving history.
	events, err := tx.Query("SELECT event FROM execution_events WHERE session_id=? ORDER BY seq", previous)
	if err != nil {
		return nil, nil, err
	}
	for events.Next() {
		var b []byte
		if err = events.Scan(&b); err != nil {
			break
		}
		event := new(aop.Event)
		if err = proto.Unmarshal(b, event); err != nil {
			break
		}
		if message := event.GetMessage(); message != nil {
			history = append(history, message)
		} else if result := event.GetToolResult(); result != nil {
			history = append(history, &aop.Message{Role: "tool", Content: []*aop.Content{{Value: &aop.Content_ToolResult{ToolResult: result}}}})
		}
	}
	err = errors.Join(err, events.Err(), events.Close())
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query("SELECT seq,body FROM inputs WHERE task_id=? AND state='pending' ORDER BY seq LIMIT ?", t.ID, maxInputs)
	if err != nil {
		return nil, nil, err
	}
	var inputs []taskInput
	for rows.Next() {
		var i taskInput
		var b []byte
		if err = rows.Scan(&i.seq, &b); err != nil {
			break
		}
		i.message, err = decodeMessage(b)
		if err != nil {
			break
		}
		inputs = append(inputs, i)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, nil, err
	}
	if len(inputs) == 0 {
		return nil, nil, sql.ErrNoRows
	}
	data, err = encodeHistory(history)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec("UPDATE inputs SET state='running',session_id=? WHERE task_id=? AND state='pending' AND seq<=?", sessionID, t.ID, inputs[len(inputs)-1].seq); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec("UPDATE tasks SET state='running',session_id=?,history=? WHERE id=?", sessionID, data, t.ID); err != nil {
		return nil, nil, err
	}
	// The recovered journal is now included in the snapshot. Retire it in
	// this transaction so failed claims retain all recovery evidence.
	if _, err = tx.Exec("DELETE FROM execution_events WHERE session_id=?", previous); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec("INSERT INTO attempts(session_id,task_id,state) VALUES(?,?,'running')", sessionID, t.ID); err != nil {
		return nil, nil, err
	}
	return inputs, history, tx.Commit()
}

func (s *activeStore) complete(t taskRecord, inputs []taskInput, history []*aop.Message, status string, feedback *mailbox.Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := encodeHistory(history)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tasks SET state=?,history=? WHERE id=?", status, b, t.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE inputs SET state='pending' WHERE task_id=? AND session_id=? AND state='running'", t.ID, t.SessionID); err != nil {
		return err
	}
	for _, input := range inputs {
		state := "done"
		if status == "interrupted" {
			state = "pending"
		}
		if _, err = tx.Exec("UPDATE inputs SET state=? WHERE seq=?", state, input.seq); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE attempts SET state=? WHERE session_id=?", status, t.SessionID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM execution_events WHERE session_id=?", t.SessionID); err != nil {
		return err
	}
	if status != "canceled" && status != "interrupted" {
		if _, err = tx.Exec("UPDATE tasks SET state='queued' WHERE id=? AND EXISTS(SELECT 1 FROM inputs WHERE task_id=? AND state='pending')", t.ID, t.ID); err != nil {
			return err
		}
	}
	if feedback != nil {
		body, encodeErr := encodeMessage(*feedback)
		if encodeErr != nil {
			return encodeErr
		}
		key := messageKey([4]string{"system", "subagent", t.ID, feedback.Message.Id})
		if _, err = tx.Exec("INSERT OR IGNORE INTO messages(key,id,body) VALUES(?,?,?)", key, feedback.Message.Id, body); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *activeStore) journal(event *aop.Event) error {
	b, err := proto.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO execution_events(session_id,seq,event) VALUES(?,?,?)", event.SessionId, event.Seq, b)
	return err
}
func (s *activeStore) value(key string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}
func (s *activeStore) setValue(key string, value []byte) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO kv(key,value) VALUES(?,?)", key, value)
	return err
}
