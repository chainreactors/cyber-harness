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
	Report    string `bun:"report,notnull"`
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

type requestJournalModel struct {
	bun.BaseModel `bun:"table:aop_request_journal,alias:request_journal"`

	RequestID    string `bun:"request_id,pk"`
	Method       string `bun:"method,notnull"`
	RequestHash  []byte `bun:"request_hash,type:blob,notnull"`
	ResponseJSON string `bun:"response_json,type:text,notnull"`
	CreatedAt    string `bun:"created_at,notnull"`
}

type scoNodeModel struct {
	bun.BaseModel `bun:"table:sco_nodes,alias:node"`

	CSTXID    string `bun:"cstx_id,pk"`
	CSTXType  string `bun:"cstx_type,notnull"`
	Data      string `bun:"data,type:text,notnull"`
	CreatedAt string `bun:"created_at,notnull"`
	UpdatedAt string `bun:"updated_at,notnull"`
}

type scoObservationModel struct {
	bun.BaseModel `bun:"table:sco_observations,alias:observation"`

	OperationID string        `bun:"operation_id,pk"`
	CSTXID      string        `bun:"cstx_id,pk"`
	ObservedAt  string        `bun:"observed_at,notnull"`
	Node        *scoNodeModel `bun:"rel:belongs-to,join:cstx_id=cstx_id,on_delete:cascade"`
}
