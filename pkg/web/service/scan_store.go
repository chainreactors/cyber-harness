package service

import (
	"context"
	"fmt"

	scanpb "github.com/chainreactors/cyber/pkg/web/scan"
	"github.com/uptrace/bun"
)

// ScanSchema is the scan console's storage module: scan records plus the
// session junction. Hosts without a scan console open the store without it.
var ScanSchema = SchemaModule{
	Name:    "scan",
	Models:  []any{(*scanModel)(nil), (*sessionScanModel)(nil)},
	Indexes: []SchemaIndex{{Model: (*scanModel)(nil), Name: "idx_scans_created", Expr: "created_at DESC"}},
	Tables: map[string][]string{
		"scans":         {"id", "target", "mode", "verify", "sniper", "status", "progress", "error", "scan_json", "created_at", "updated_at"},
		"session_scans": {"session_id", "scan_id"},
	},
}

type scanModel struct {
	bun.BaseModel `bun:"table:scans,alias:scan"`

	ID        string `bun:"id,pk"`
	Target    string `bun:"target,notnull"`
	Mode      string `bun:"mode,notnull"`
	Verify    *bool  `bun:"verify"`
	Sniper    bool   `bun:"sniper,notnull"`
	Status    string `bun:"status,notnull"`
	Progress  string `bun:"progress,notnull"`
	Error     string `bun:"error,notnull"`
	ScanJSON  string `bun:"scan_json,type:text,notnull"`
	CreatedAt string `bun:"created_at,notnull"`
	UpdatedAt string `bun:"updated_at,notnull"`
}

type sessionScanModel struct {
	bun.BaseModel `bun:"table:session_scans,alias:session_scan"`

	SessionID string        `bun:"session_id,pk"`
	ScanID    string        `bun:"scan_id,pk"`
	Session   *sessionModel `bun:"rel:belongs-to,join:session_id=id,on_delete:cascade"`
	Scan      *scanModel    `bun:"rel:belongs-to,join:scan_id=id,on_delete:cascade"`
}

// customizeCreateTable declares the junction's two constraints through the
// schema builder: bun deliberately avoids inferring foreign keys from
// composite-PK junction tables unless they are registered as a many-to-many
// relation, and this table is queried directly.
func (*sessionScanModel) customizeCreateTable(query *bun.CreateTableQuery) *bun.CreateTableQuery {
	return query.
		ForeignKey("(session_id) REFERENCES chat_sessions(id) ON DELETE CASCADE").
		ForeignKey("(scan_id) REFERENCES scans(id) ON DELETE CASCADE")
}

func (s *SQLiteStore) Create(ctx context.Context, scan *scanpb.Scan) error {
	model, err := scanToModel(scan)
	if err != nil {
		return err
	}
	_, err = s.orm.NewInsert().Model(model).Exec(ctx)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*scanpb.Scan, error) {
	var model scanModel
	if err := s.orm.NewSelect().Model(&model).Column("scan_json").Where("id = ?", id).Limit(1).Scan(ctx); err != nil {
		return nil, err
	}
	return scanFromJSON(model.ScanJSON)
}

func (s *SQLiteStore) List(ctx context.Context, limit int) ([]*scanpb.Scan, error) {
	if limit <= 0 {
		limit = 50
	}
	var models []scanModel
	if err := s.orm.NewSelect().Model(&models).Column("scan_json").OrderExpr("created_at DESC").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	scans := make([]*scanpb.Scan, 0, len(models))
	for _, model := range models {
		scan, err := scanFromJSON(model.ScanJSON)
		if err != nil {
			return nil, err
		}
		scans = append(scans, scan)
	}
	return scans, nil
}

func (s *SQLiteStore) Update(ctx context.Context, scan *scanpb.Scan) error {
	model, err := scanToModel(scan)
	if err != nil {
		return err
	}
	_, err = s.orm.NewUpdate().Model(model).
		Column("target", "mode", "verify", "sniper", "status", "progress", "error", "scan_json", "updated_at").
		WherePK().Exec(ctx)
	return err
}

func (s *SQLiteStore) TransitionScan(ctx context.Context, scan *scanpb.Scan, expected ...scanpb.ScanStatus) (bool, error) {
	if scan == nil {
		return false, fmt.Errorf("scan is required")
	}
	if len(expected) == 0 {
		return false, fmt.Errorf("at least one expected scan status is required")
	}
	model, err := scanToModel(scan)
	if err != nil {
		return false, err
	}
	statuses := make([]string, len(expected))
	for i, status := range expected {
		statuses[i] = scanStatusToDB(status)
	}
	result, err := s.orm.NewUpdate().Model(model).
		Column("target", "mode", "verify", "sniper", "status", "progress", "error", "scan_json", "updated_at").
		Where("id = ?", model.ID).Where("status IN (?)", bun.List(statuses)).Exec(ctx)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.orm.NewDelete().Model((*scanModel)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func scanToModel(scan *scanpb.Scan) (*scanModel, error) {
	if scan == nil {
		return nil, fmt.Errorf("scan is required")
	}
	raw, err := marshalProtoJSON(scan)
	if err != nil {
		return nil, err
	}
	options := scan.GetOptions()
	if options == nil {
		options = &scanpb.ScanOptions{}
	}
	return &scanModel{
		ID: scan.GetId(), Target: scan.GetTarget(), Mode: scan.GetMode(),
		Verify: options.Verify, Sniper: options.GetSniper(),
		Status: scanStatusToDB(scan.GetStatus()), Progress: scan.GetProgress(),
		Error: scan.GetError(), ScanJSON: raw,
		CreatedAt: formatProtoTime(scan.GetCreatedAt()), UpdatedAt: formatProtoTime(scan.GetUpdatedAt()),
	}, nil
}

func scanFromJSON(raw string) (*scanpb.Scan, error) {
	scan := new(scanpb.Scan)
	if err := unmarshalProtoJSON(raw, scan, "scan"); err != nil {
		return nil, err
	}
	return scan, nil
}

func (s *SQLiteStore) LinkScanToSession(ctx context.Context, sessionID, scanID string) error {
	_, err := s.orm.NewInsert().Model(&sessionScanModel{SessionID: sessionID, ScanID: scanID}).
		On("CONFLICT (session_id, scan_id) DO NOTHING").Exec(ctx)
	return err
}

func (s *SQLiteStore) SessionScanIDs(ctx context.Context, sessionID string) ([]string, error) {
	var ids []string
	err := s.orm.NewSelect().Model((*sessionScanModel)(nil)).Column("scan_id").
		Where("session_id = ?", sessionID).Scan(ctx, &ids)
	return ids, err
}

// ScanSessionIDs lists the sessions a scan is bound to. A session binds a scan
// at open time (SessionBinding), so this is what a finishing scan needs to
// address its result card; SessionScanIDs is the same relation read the other
// way, from a session.
func (s *SQLiteStore) ScanSessionIDs(ctx context.Context, scanID string) ([]string, error) {
	var ids []string
	err := s.orm.NewSelect().Model((*sessionScanModel)(nil)).Column("session_id").
		Where("scan_id = ?", scanID).Scan(ctx, &ids)
	return ids, err
}
