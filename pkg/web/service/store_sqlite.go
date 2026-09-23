package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db      *sql.DB
	orm     *bun.DB
	modules []SchemaModule
}

var (
	dbJSONMarshal   = protojson.MarshalOptions{UseProtoNames: true}
	dbJSONUnmarshal = protojson.UnmarshalOptions{}
)

func NewSQLiteStore(dbPath string, modules ...SchemaModule) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	orm := bun.NewDB(db, sqlitedialect.New())
	if err := initializeSchema(orm, db, schemaUnion(modules)); err != nil {
		_ = orm.Close()
		return nil, fmt.Errorf("initialize latest sqlite schema: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("verify sqlite foreign keys: %w", err)
		}
		return nil, fmt.Errorf("verify sqlite foreign keys: disabled")
	}
	return &SQLiteStore{db: db, orm: orm, modules: modules}, nil
}

// hasModule reports whether the store carries a module's tables.
func (s *SQLiteStore) hasModule(name string) bool {
	for _, module := range s.modules {
		if module.Name == name {
			return true
		}
	}
	return false
}

// initializeSchema creates the only supported schema for an empty database.
// Existing databases must already match it exactly; there are no migrations
// or historical schema versions before the first release.
func initializeSchema(orm *bun.DB, db *sql.DB, schema SchemaModule) error {
	tables, err := schemaTables(db)
	if err != nil {
		return err
	}
	if len(tables) != 0 {
		return validateSchema(db, schema)
	}
	if err := createSchema(context.Background(), orm, schema); err != nil {
		return err
	}
	return validateSchema(db, schema)
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
	var model requestLedgerModel
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
	_, err = s.orm.NewInsert().Model(&requestLedgerModel{
		RequestID: requestID, Method: method, RequestHash: requestHash,
		ResponseJSON: raw, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}).Exec(ctx)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.orm.Close()
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
	if s.hasModule(ScanSchema.Name) {
		session.ScanIds, _ = s.SessionScanIDs(ctx, id)
	}
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
	if s.hasModule(ScanSchema.Name) {
		for _, session := range sessions {
			scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
			session.ScanIds = scanIDs
		}
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
	if s.hasModule(ScanSchema.Name) {
		scanIDs, _ := s.SessionScanIDs(ctx, session.GetSession().GetId())
		if len(scanIDs) > 0 {
			session.ScanIds = scanIDs
		}
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
	if strings.TrimSpace(sessionID) == "" {
		return 0, false, fmt.Errorf("AOP event session_id is required")
	}
	if strings.TrimSpace(event.Id) == "" {
		return 0, false, fmt.Errorf("AOP event id is required")
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
		var existing aopEventModel
		lookupErr := tx.NewSelect().Model(&existing).Column("cursor").
			Where("session_id = ? AND event_id = ?", sessionID, event.Id).Limit(1).Scan(ctx)
		if lookupErr == nil {
			cursor = existing.Cursor
			persisted = false
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return lookupErr
		}
		if err := tx.NewSelect().Model((*aopEventModel)(nil)).
			ColumnExpr("COALESCE(MAX(cursor), 0) + 1").Where("session_id = ?", sessionID).Scan(ctx, &cursor); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&aopEventModel{
			ID: generateID(), SessionID: sessionID, EventID: event.Id, Cursor: cursor,
			TurnID: event.GetTurnId(), Emitter: event.GetEmitter(), Sequence: event.GetSeq(),
			EventJSON: raw, CreatedAt: createdAt,
		}).Exec(ctx)
		if err == nil {
			persisted = true
		}
		return err
	})
	if err != nil {
		return 0, false, err
	}
	return cursor, persisted, nil
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

const artifactSyncBatch = 100

// SyncArtifactEvents appends raw Artifact events and returns the next archive
// page. CSTX parsing and projection belong to the browser.
func (s *SQLiteStore) SyncArtifactEvents(ctx context.Context, appended []*aop.Event, after int64) ([]*aop.EventDelivery, error) {
	models, err := artifactEventModels(appended)
	if err != nil {
		return nil, err
	}

	var deliveries []*aop.EventDelivery
	err = s.orm.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := insertArtifactModels(ctx, tx, models); err != nil {
			return err
		}
		var archived []rawArtifactModel
		if err := tx.NewSelect().Model(&archived).Where("cursor > ?", after).
			OrderExpr("cursor ASC").Limit(artifactSyncBatch).Scan(ctx); err != nil {
			return err
		}
		deliveries = make([]*aop.EventDelivery, 0, len(archived))
		for _, model := range archived {
			event := new(aop.Event)
			if err := protobuf.Unmarshal(model.EventProto, event); err != nil {
				return fmt.Errorf("decode artifact event %q: %w", model.EventID, err)
			}
			deliveries = append(deliveries, &aop.EventDelivery{
				Cursor: strconv.FormatInt(model.Cursor, 10),
				Event:  event,
			})
		}
		return nil
	})
	return deliveries, err
}

func (s *SQLiteStore) archiveArtifactEvents(ctx context.Context, appended []*aop.Event) error {
	models, err := artifactEventModels(appended)
	if err != nil {
		return err
	}
	return s.orm.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return insertArtifactModels(ctx, tx, models)
	})
}

func artifactEventModels(appended []*aop.Event) ([]*rawArtifactModel, error) {
	models := make([]*rawArtifactModel, 0, len(appended))
	for index, event := range appended {
		if event == nil {
			return nil, fmt.Errorf("artifact event %d is required", index)
		}
		if _, _, found, err := toolpb.FromEvent(event); err != nil {
			return nil, fmt.Errorf("artifact event %d: %w", index, err)
		} else if !found {
			return nil, fmt.Errorf("artifact event %d does not contain an aop.tool.Artifact payload", index)
		}
		eventID := strings.TrimSpace(event.GetId())
		if eventID == "" {
			return nil, fmt.Errorf("artifact event %d has no id", index)
		}
		if event.GetEmittedAt() == nil || !event.GetEmittedAt().IsValid() {
			return nil, fmt.Errorf("artifact event %q has an invalid emitted_at", eventID)
		}
		raw, err := (protobuf.MarshalOptions{Deterministic: true}).Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode artifact event %q: %w", eventID, err)
		}
		models = append(models, &rawArtifactModel{
			EventID:    eventID,
			EventProto: raw,
			CreatedAt:  event.GetEmittedAt().AsTime().UTC().Format(time.RFC3339Nano),
		})
	}
	return models, nil
}

func insertArtifactModels(ctx context.Context, tx bun.Tx, models []*rawArtifactModel) error {
	for _, model := range models {
		if _, err := tx.NewInsert().Model(model).On("CONFLICT (event_id) DO NOTHING").Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
