package web

import (
	"context"

	"github.com/chainreactors/aiscan/core/output"
)

type Store interface {
	// Scan CRUD
	Create(ctx context.Context, job *ScanJob) error
	Get(ctx context.Context, id string) (*ScanJob, error)
	List(ctx context.Context, limit int) ([]*ScanJob, error)
	Update(ctx context.Context, job *ScanJob) error
	Delete(ctx context.Context, id string) error

	// Chat sessions
	CreateSession(ctx context.Context, session *ChatSession) error
	GetSession(ctx context.Context, id string) (*ChatSession, error)
	ListSessions(ctx context.Context, limit int) ([]*ChatSession, error)
	UpdateSession(ctx context.Context, session *ChatSession) error
	DeleteSession(ctx context.Context, id string) error

	// Chat messages
	AddMessage(ctx context.Context, msg *ChatMessage) error
	ListMessages(ctx context.Context, sessionID string, limit int) ([]*ChatMessage, error)

	// Session-scan association
	LinkScanToSession(ctx context.Context, sessionID, scanID string) error
	SessionScanIDs(ctx context.Context, sessionID string) ([]string, error)

	// Records
	InsertRecord(ctx context.Context, rec *output.Record) error
	InsertRecords(ctx context.Context, recs []*output.Record) error
	ListRecords(ctx context.Context, filter output.RecordFilter) ([]*output.Record, error)
	AggregateRecords(ctx context.Context, filter output.RecordFilter) (*output.RecordSummary, error)
}
