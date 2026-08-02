package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db  *sql.DB
	orm *bun.DB
}

const sqliteSchemaVersion = 3

var (
	dbJSONMarshal   = protojson.MarshalOptions{UseProtoNames: true}
	dbJSONUnmarshal = protojson.UnmarshalOptions{}
)

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	orm := bun.NewDB(db, sqlitedialect.New())
	if err := migrate(orm, db); err != nil {
		_ = orm.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("verify sqlite foreign keys: %w", err)
		}
		return nil, fmt.Errorf("verify sqlite foreign keys: disabled")
	}
	return &SQLiteStore{db: db, orm: orm}, nil
}

func migrate(orm *bun.DB, db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == sqliteSchemaVersion {
		return nil
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

	return orm.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		models := []any{
			(*scanModel)(nil),
			(*sessionModel)(nil),
			(*aopEventModel)(nil),
			(*sessionScanModel)(nil),
			(*requestJournalModel)(nil),
			(*scoNodeModel)(nil),
			(*scoObservationModel)(nil),
		}
		for _, model := range models {
			query := tx.NewCreateTable().Model(model).WithForeignKeys()
			switch model.(type) {
			case *sessionScanModel:
				// Bun deliberately avoids inferring foreign keys from composite-PK
				// junction tables unless they are registered as a many-to-many
				// relation. This table is queried directly, so keep the model simple
				// and declare its two constraints through the schema builder.
				query = query.
					ForeignKey("(session_id) REFERENCES chat_sessions(id) ON DELETE CASCADE").
					ForeignKey("(scan_id) REFERENCES scans(id) ON DELETE CASCADE")
			case *scoObservationModel:
				query = query.ForeignKey("(cstx_id) REFERENCES sco_nodes(cstx_id) ON DELETE CASCADE")
			}
			if _, err := query.Exec(ctx); err != nil {
				return err
			}
		}
		indexes := []*bun.CreateIndexQuery{
			tx.NewCreateIndex().Model((*scanModel)(nil)).Index("idx_scans_created").ColumnExpr("created_at DESC"),
			tx.NewCreateIndex().Model((*sessionModel)(nil)).Index("idx_sessions_updated").ColumnExpr("updated_at DESC"),
			tx.NewCreateIndex().Model((*sessionModel)(nil)).Index("idx_sessions_node_id").Column("node_id"),
			tx.NewCreateIndex().Model((*aopEventModel)(nil)).Index("idx_aop_events_session").Column("session_id", "cursor"),
			tx.NewCreateIndex().Model((*aopEventModel)(nil)).Index("idx_aop_events_turn").Column("turn_id"),
			tx.NewCreateIndex().Model((*scoNodeModel)(nil)).Index("idx_sco_nodes_type").Column("cstx_type"),
			tx.NewCreateIndex().Model((*scoObservationModel)(nil)).Index("idx_sco_observations_node").Column("cstx_id"),
		}
		for _, index := range indexes {
			if _, err := index.Exec(ctx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, sqliteSchemaVersion))
		return err
	})
}

func marshalProtoJSON(message protobuf.Message) (string, error) {
	if message == nil {
		return "", fmt.Errorf("protobuf message is required")
	}
	raw, err := dbJSONMarshal.Marshal(message)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalProtoJSON(raw string, message protobuf.Message, kind string) error {
	if err := dbJSONUnmarshal.Unmarshal([]byte(raw), message); err != nil {
		return fmt.Errorf("decode %s protojson: %w", kind, err)
	}
	return nil
}

func (s *SQLiteStore) LoadAOPRequest(ctx context.Context, requestID, method string, requestHash []byte, response protobuf.Message) (found, conflict bool, err error) {
	if s == nil || strings.TrimSpace(requestID) == "" || response == nil {
		return false, false, nil
	}
	var model requestJournalModel
	err = s.orm.NewSelect().Model(&model).Where("request_id = ?", requestID).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if model.Method != method || !bytes.Equal(model.RequestHash, requestHash) {
		return false, true, nil
	}
	if err := unmarshalProtoJSON(model.ResponseJSON, response, "AOP response"); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *SQLiteStore) SaveAOPRequest(ctx context.Context, requestID, method string, requestHash []byte, response protobuf.Message) error {
	if s == nil || strings.TrimSpace(requestID) == "" || response == nil {
		return nil
	}
	raw, err := marshalProtoJSON(response)
	if err != nil {
		return err
	}
	_, err = s.orm.NewInsert().Model(&requestJournalModel{
		RequestID: requestID, Method: method, RequestHash: requestHash,
		ResponseJSON: raw, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}).Exec(ctx)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.orm.Close()
}

func (s *SQLiteStore) Create(ctx context.Context, scan *types.Scan) error {
	model, err := scanToModel(scan)
	if err != nil {
		return err
	}
	_, err = s.orm.NewInsert().Model(model).Exec(ctx)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*types.Scan, error) {
	var model scanModel
	if err := s.orm.NewSelect().Model(&model).Column("scan_json").Where("id = ?", id).Limit(1).Scan(ctx); err != nil {
		return nil, err
	}
	return scanFromJSON(model.ScanJSON)
}

func (s *SQLiteStore) List(ctx context.Context, limit int) ([]*types.Scan, error) {
	if limit <= 0 {
		limit = 50
	}
	var models []scanModel
	if err := s.orm.NewSelect().Model(&models).Column("scan_json").OrderExpr("created_at DESC").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	scans := make([]*types.Scan, 0, len(models))
	for _, model := range models {
		scan, err := scanFromJSON(model.ScanJSON)
		if err != nil {
			return nil, err
		}
		scans = append(scans, scan)
	}
	return scans, nil
}

func (s *SQLiteStore) Update(ctx context.Context, scan *types.Scan) error {
	model, err := scanToModel(scan)
	if err != nil {
		return err
	}
	_, err = s.orm.NewUpdate().Model(model).
		Column("target", "mode", "verify", "sniper", "deep", "status", "progress", "report", "error", "scan_json", "updated_at").
		WherePK().Exec(ctx)
	return err
}

func (s *SQLiteStore) TransitionScan(ctx context.Context, scan *types.Scan, expected ...types.ScanStatus) (bool, error) {
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
		Column("target", "mode", "verify", "sniper", "deep", "status", "progress", "report", "error", "scan_json", "updated_at").
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

func scanToModel(scan *types.Scan) (*scanModel, error) {
	if scan == nil {
		return nil, fmt.Errorf("scan is required")
	}
	raw, err := marshalProtoJSON(scan)
	if err != nil {
		return nil, err
	}
	options := scan.GetOptions()
	return &scanModel{
		ID: scan.GetId(), Target: scan.GetTarget(), Mode: scan.GetMode(),
		Verify: options.GetVerify(), Sniper: options.GetSniper(), Deep: options.GetDeep(),
		Status: scanStatusToDB(scan.GetStatus()), Progress: scan.GetProgress(),
		Report: scan.GetReport(), Error: scan.GetError(), ScanJSON: raw,
		CreatedAt: formatProtoTime(scan.GetCreatedAt()), UpdatedAt: formatProtoTime(scan.GetUpdatedAt()),
	}, nil
}

func scanFromJSON(raw string) (*types.Scan, error) {
	scan := new(types.Scan)
	if err := unmarshalProtoJSON(raw, scan, "scan"); err != nil {
		return nil, err
	}
	return scan, nil
}

func formatProtoTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}

func (s *SQLiteStore) CreateSession(ctx context.Context, session *types.SessionRecord) error {
	model, err := sessionToModel(session)
	if err != nil {
		return err
	}
	_, err = s.orm.NewInsert().Model(model).Exec(ctx)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*types.SessionRecord, error) {
	var model sessionModel
	if err := s.orm.NewSelect().Model(&model).Column("session_json").Where("id = ?", id).Limit(1).Scan(ctx); err != nil {
		return nil, err
	}
	session, err := sessionFromJSON(model.SessionJSON)
	if err != nil {
		return nil, err
	}
	session.ScanIds, _ = s.SessionScanIDs(ctx, id)
	return session, nil
}

func (s *SQLiteStore) ListSessions(ctx context.Context, limit int) ([]*types.SessionRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	var models []sessionModel
	if err := s.orm.NewSelect().Model(&models).Column("session_json").OrderExpr("updated_at DESC").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	return sessionsFromModels(models)
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
	query := s.orm.NewSelect().Model((*sessionModel)(nil)).Column("session_json").OrderExpr("updated_at DESC").Limit(limit + 1).Offset(offset)
	if !includeClosed {
		query = query.Where("status = ?", SessionStateOpen)
	}
	var models []sessionModel
	if err := query.Model(&models).Scan(ctx); err != nil {
		return nil, false, err
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	sessions, err := sessionsFromModels(models)
	if err != nil {
		return nil, false, err
	}
	for _, session := range sessions {
		scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
		session.ScanIds = scanIDs
	}
	return sessions, hasMore, nil
}

func sessionsFromModels(models []sessionModel) ([]*types.SessionRecord, error) {
	sessions := make([]*types.SessionRecord, 0, len(models))
	for _, model := range models {
		session, err := sessionFromJSON(model.SessionJSON)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func sessionFromJSON(raw string) (*types.SessionRecord, error) {
	session := new(types.SessionRecord)
	if err := unmarshalProtoJSON(raw, session, "session"); err != nil {
		return nil, err
	}
	return session, nil
}

func sessionToModel(session *types.SessionRecord) (*sessionModel, error) {
	if session == nil || session.GetSession() == nil {
		return nil, fmt.Errorf("session is required")
	}
	raw, err := marshalProtoJSON(session)
	if err != nil {
		return nil, err
	}
	domain := session.GetSession()
	return &sessionModel{
		ID: domain.GetId(), NodeID: domain.GetNodeId(), Status: domain.GetState(),
		Title: domain.GetTitle(), AgentName: session.GetAgentName(), SessionJSON: raw,
		CreatedAt: formatProtoTime(session.GetCreatedAt()), UpdatedAt: formatProtoTime(session.GetUpdatedAt()),
	}, nil
}

func (s *SQLiteStore) UpdateSession(ctx context.Context, session *types.SessionRecord) error {
	scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
	if len(scanIDs) > 0 {
		session.ScanIds = scanIDs
	}
	model, err := sessionToModel(session)
	if err != nil {
		return err
	}
	_, err = s.orm.NewUpdate().Model(model).
		Column("node_id", "status", "title", "agent_name", "session_json", "updated_at").WherePK().Exec(ctx)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.orm.NewDelete().Model((*sessionModel)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (s *SQLiteStore) AddAOPEvent(ctx context.Context, sessionID string, event *aop.Event) error {
	_, _, err := s.AppendAOPEvent(ctx, sessionID, event)
	return err
}

func (s *SQLiteStore) AppendAOPEvent(ctx context.Context, sessionID string, event *aop.Event) (cursor int64, persisted bool, err error) {
	if event == nil || event.GetMessageDelta() != nil || event.GetToolCallDelta() != nil {
		return 0, false, nil
	}
	raw, err := marshalProtoJSON(event)
	if err != nil {
		return 0, false, err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if event.GetEmittedAt() != nil {
		createdAt = event.GetEmittedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	err = s.orm.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewSelect().Model((*aopEventModel)(nil)).
			ColumnExpr("COALESCE(MAX(cursor), 0) + 1").Where("session_id = ?", sessionID).Scan(ctx, &cursor); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&aopEventModel{
			ID: generateID(), SessionID: sessionID, Cursor: cursor,
			TurnID: event.GetTurnId(), Emitter: event.GetEmitter(), Sequence: event.GetSeq(),
			EventJSON: raw, CreatedAt: createdAt,
		}).Exec(ctx)
		return err
	})
	if err != nil {
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
	var maximum uint64
	err := s.orm.NewSelect().Model((*aopEventModel)(nil)).
		ColumnExpr("COALESCE(MAX(sequence), 0)").Where("session_id = ?", sessionID).Scan(ctx, &maximum)
	return maximum, err
}

func (s *SQLiteStore) ListAOPEventPage(ctx context.Context, sessionID string, before int64, limit int) ([]*aop.EventDelivery, int64, error) {
	if limit <= 0 {
		limit = 10000
	}
	if limit > 10000 {
		limit = 10000
	}
	query := s.orm.NewSelect().Model((*aopEventModel)(nil)).Column("cursor", "event_json").
		Where("session_id = ?", sessionID).OrderExpr("cursor DESC").Limit(limit + 1)
	if before > 0 {
		query = query.Where("cursor < ?", before)
	}
	var models []aopEventModel
	if err := query.Model(&models).Scan(ctx); err != nil {
		return nil, 0, err
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	events := make([]*aop.EventDelivery, 0, len(models))
	for i := len(models) - 1; i >= 0; i-- {
		event, err := eventFromJSON(models[i].EventJSON)
		if err != nil || event.GetSessionId() == "" || event.GetPayload() == nil {
			continue
		}
		events = append(events, &aop.EventDelivery{Cursor: strconv.FormatInt(models[i].Cursor, 10), Event: event})
	}
	var next int64
	if hasMore && len(events) > 0 {
		next, _ = strconv.ParseInt(events[0].Cursor, 10, 64)
	}
	return events, next, nil
}

func (s *SQLiteStore) ListAOPEventsAfter(ctx context.Context, sessionID string, after int64, limit int) ([]*aop.EventDelivery, error) {
	if after <= 0 {
		events, _, err := s.ListAOPEventPage(ctx, sessionID, 0, limit)
		return events, err
	}
	query := s.orm.NewSelect().Model((*aopEventModel)(nil)).Column("cursor", "event_json").
		Where("session_id = ? AND cursor > ?", sessionID, after).OrderExpr("cursor ASC")
	if limit > 0 {
		if limit > 10000 {
			limit = 10000
		}
		query = query.Limit(limit)
	}
	var models []aopEventModel
	if err := query.Model(&models).Scan(ctx); err != nil {
		return nil, err
	}
	events := make([]*aop.EventDelivery, 0, len(models))
	for _, model := range models {
		event, err := eventFromJSON(model.EventJSON)
		if err != nil || event.GetSessionId() == "" || event.GetPayload() == nil {
			continue
		}
		events = append(events, &aop.EventDelivery{Cursor: strconv.FormatInt(model.Cursor, 10), Event: event})
	}
	return events, nil
}

func eventFromJSON(raw string) (*aop.Event, error) {
	event := new(aop.Event)
	if err := unmarshalProtoJSON(raw, event, "AOP event"); err != nil {
		return nil, err
	}
	return event, nil
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

func (s *SQLiteStore) UpsertSCONodes(ctx context.Context, operationID string, nodes []json.RawMessage) error {
	return s.orm.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, raw := range nodes {
			var header struct {
				Type string `json:"cstx_type"`
				ID   string `json:"cstx_id"`
			}
			if json.Unmarshal(raw, &header) != nil || header.ID == "" {
				continue
			}
			node := &scoNodeModel{CSTXID: header.ID, CSTXType: header.Type, Data: string(raw), CreatedAt: now, UpdatedAt: now}
			if _, err := tx.NewInsert().Model(node).On("CONFLICT (cstx_id) DO UPDATE").
				Set("cstx_type = EXCLUDED.cstx_type").Set("data = EXCLUDED.data").Set("updated_at = EXCLUDED.updated_at").Exec(ctx); err != nil {
				return err
			}
			if operationID != "" {
				if _, err := tx.NewInsert().Model(&scoObservationModel{OperationID: operationID, CSTXID: header.ID, ObservedAt: now}).
					On("CONFLICT (operation_id, cstx_id) DO NOTHING").Exec(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *SQLiteStore) ListSCONodes(ctx context.Context, nodeType string, limit int) ([]json.RawMessage, error) {
	return s.ListSCONodesByScanID(ctx, "", nodeType, limit)
}

func (s *SQLiteStore) ListSCONodesByScanID(ctx context.Context, scanID, nodeType string, limit int) ([]json.RawMessage, error) {
	query := s.orm.NewSelect().Model((*scoNodeModel)(nil)).Column("node.data")
	if scanID != "" {
		query = query.Join("JOIN sco_observations AS observation ON observation.cstx_id = node.cstx_id").
			Where("observation.operation_id = ?", scanID)
	}
	if nodeType != "" {
		query = query.Where("node.cstx_type = ?", nodeType)
	}
	if limit >= 0 {
		query = query.Limit(limit)
	}
	var models []scoNodeModel
	if err := query.Model(&models).OrderExpr("node.updated_at DESC").Scan(ctx); err != nil {
		return nil, err
	}
	nodes := make([]json.RawMessage, 0, len(models))
	for _, model := range models {
		nodes = append(nodes, json.RawMessage(model.Data))
	}
	return nodes, nil
}

func (s *SQLiteStore) GetSCONode(ctx context.Context, cstxID string) (json.RawMessage, error) {
	var model scoNodeModel
	if err := s.orm.NewSelect().Model(&model).Column("data").Where("cstx_id = ?", cstxID).Limit(1).Scan(ctx); err != nil {
		return nil, err
	}
	return json.RawMessage(model.Data), nil
}

func (s *SQLiteStore) DeleteSCONodesByScan(ctx context.Context, scanID string) error {
	_, err := s.orm.NewDelete().Model((*scoObservationModel)(nil)).Where("operation_id = ?", scanID).Exec(ctx)
	return err
}

func (s *SQLiteStore) SCONodeStats(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		CSTXType string `bun:"cstx_type"`
		Count    int    `bun:"count"`
	}
	if err := s.orm.NewSelect().Model((*scoNodeModel)(nil)).Column("cstx_type").
		ColumnExpr("COUNT(*) AS count").Group("cstx_type").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	stats := make(map[string]int, len(rows))
	for _, row := range rows {
		stats[row.CSTXType] = row.Count
	}
	return stats, nil
}
