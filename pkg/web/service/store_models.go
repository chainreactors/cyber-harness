package service

import "github.com/uptrace/bun"

// Relational columns are the queryable projection of each domain message.
// The complete protobuf value is stored as protojson in the *_json column, so
// protobuf-only additions need no schema change unless they must be indexed.

type sessionModel struct {
	bun.BaseModel `bun:"table:chat_sessions,alias:session"`

	ID          string `bun:"id,pk"`
	NodeID      string `bun:"node_id,notnull"`
	Status      string `bun:"status,notnull"`
	Title       string `bun:"title,notnull"`
	AgentName   string `bun:"agent_name,notnull"`
	SessionJSON string `bun:"session_json,type:text,notnull"`
	CreatedAt   string `bun:"created_at,notnull"`
	UpdatedAt   string `bun:"updated_at,notnull"`
}

type aopEventModel struct {
	bun.BaseModel `bun:"table:chat_aop_events,alias:event"`

	ID        string        `bun:"id,pk"`
	SessionID string        `bun:"session_id,notnull,unique:aop_event_cursor"`
	EventID   string        `bun:"event_id,notnull"`
	Cursor    int64         `bun:"cursor,notnull,unique:aop_event_cursor"`
	TurnID    string        `bun:"turn_id,notnull"`
	Emitter   string        `bun:"emitter,notnull"`
	Sequence  uint64        `bun:"sequence,notnull"`
	EventJSON string        `bun:"event_json,type:text,notnull"`
	CreatedAt string        `bun:"created_at,notnull"`
	Session   *sessionModel `bun:"rel:belongs-to,join:session_id=id,on_delete:cascade"`
}

type requestLedgerModel struct {
	bun.BaseModel `bun:"table:aop_request_ledger,alias:request_ledger"`

	RequestID    string `bun:"request_id,pk"`
	Method       string `bun:"method,notnull"`
	RequestHash  []byte `bun:"request_hash,type:blob,notnull"`
	ResponseJSON string `bun:"response_json,type:text,notnull"`
	CreatedAt    string `bun:"created_at,notnull"`
}

type rawArtifactModel struct {
	bun.BaseModel `bun:"table:raw_artifacts,alias:artifact"`

	Cursor     int64  `bun:"cursor,pk,autoincrement"`
	EventID    string `bun:"event_id,notnull,unique"`
	EventProto []byte `bun:"event_proto,type:blob,notnull"`
	CreatedAt  string `bun:"created_at,notnull"`
}
