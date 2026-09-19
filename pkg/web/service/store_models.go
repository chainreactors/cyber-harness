package service

import "github.com/uptrace/bun"

// Relational columns are the queryable projection of each domain message.
// The complete protobuf value is stored as protojson in the *_json column, so
// protobuf-only additions need no schema change unless they must be indexed.

type scanModel struct {
	bun.BaseModel `bun:"table:scans,alias:scan"`

	ID        string `bun:"id,pk"`
	Target    string `bun:"target,notnull"`
	Mode      string `bun:"mode,notnull"`
	Verify    bool   `bun:"verify,notnull"`
	Sniper    bool   `bun:"sniper,notnull"`
	Deep      bool   `bun:"deep,notnull"`
	Status    string `bun:"status,notnull"`
	Progress  string `bun:"progress,notnull"`
	Error     string `bun:"error,notnull"`
	ScanJSON  string `bun:"scan_json,type:text,notnull"`
	CreatedAt string `bun:"created_at,notnull"`
	UpdatedAt string `bun:"updated_at,notnull"`
}

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

type sessionScanModel struct {
	bun.BaseModel `bun:"table:session_scans,alias:session_scan"`

	SessionID string        `bun:"session_id,pk"`
	ScanID    string        `bun:"scan_id,pk"`
	Session   *sessionModel `bun:"rel:belongs-to,join:session_id=id,on_delete:cascade"`
	Scan      *scanModel    `bun:"rel:belongs-to,join:scan_id=id,on_delete:cascade"`
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
