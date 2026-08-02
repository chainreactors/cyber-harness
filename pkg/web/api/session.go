package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	SessionStateOpen   = "open"
	SessionStateClosed = "closed"
)

var ErrTurnNotFound = errors.New("turn not found")

type RequestJournal interface {
	LoadAOPRequest(context.Context, string, string, []byte, proto.Message) (bool, bool, error)
	SaveAOPRequest(context.Context, string, string, []byte, proto.Message) error
}

type SessionStore interface {
	RequestJournal
	ListSessionPage(context.Context, int, int, bool) ([]*types.SessionRecord, bool, error)
	GetSession(context.Context, string) (*types.SessionRecord, error)
	CreateSession(context.Context, *types.SessionRecord) error
	UpdateSession(context.Context, *types.SessionRecord) error
	DeleteSession(context.Context, string) error
	LinkScanToSession(context.Context, string, string) error
	ListAOPEventsAfter(context.Context, string, int64, int) ([]*aop.EventDelivery, error)
}

// SessionRuntime is the Agent/AOP execution boundary. Sessions owns request
// semantics and persistence; the runtime only performs Agent dispatch and live
// event delivery.
type SessionRuntime interface {
	AgentInfo(string) (string, bool)
	GetScan(context.Context, string) (*types.Scan, error)
	OpenAgentSession(context.Context, string, *aop.OpenSessionRequest) error
	CloseAgentSession(context.Context, string, string, *aop.CloseSessionRequest) (bool, error)
	StartAgentTurn(string, *aop.RunTurnRequest)
	CancelAgentTurn(context.Context, string, string) error
	PublishUserMessage(string, string, *aop.Message)
	PublishSessionEvent(string, *aop.Event)
	SubscribeSessionEvents(string) (<-chan *aop.EventDelivery, func())
	DeleteSession(context.Context, string) error
	SessionMenu(string) []*types.CommandSpec
}

type Sessions struct {
	store         SessionStore
	runtime       SessionRuntime
	newID         func() string
	managementMu  sync.Mutex
	applicationMu sync.Mutex
}

func NewSessions(store SessionStore, runtime SessionRuntime, newID func() string) *Sessions {
	return &Sessions{store: store, runtime: runtime, newID: newID}
}

func (s *Sessions) ListSessions(ctx context.Context, request *types.ListSessionsRequest) (*types.ListSessionsResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "session store is unavailable")
	}
	if request == nil {
		request = new(types.ListSessionsRequest)
	}
	offset := 0
	if value := strings.TrimSpace(request.AfterCursor); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return nil, Errorf(CodeInvalidArgument, "invalid after_cursor %q", value)
		}
		offset = parsed
	}
	limit := int(request.Limit)
	if limit == 0 {
		limit = 100
	}
	sessions, more, err := s.store.ListSessionPage(ctx, offset, limit, request.IncludeClosed)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	response := &types.ListSessionsResponse{Sessions: sessions}
	if more {
		response.NextCursor = strconv.Itoa(offset + len(sessions))
	}
	return response, nil
}

func (s *Sessions) GetSession(ctx context.Context, request *types.GetSessionRequest) (*types.GetSessionResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "session store is unavailable")
	}
	if request == nil || strings.TrimSpace(request.SessionId) == "" {
		return nil, Errorf(CodeInvalidArgument, "session_id is required")
	}
	session, err := s.store.GetSession(ctx, request.SessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Errorf(CodeNotFound, "session not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &types.GetSessionResponse{Session: session}, nil
}

func (s *Sessions) OpenSession(ctx context.Context, requestID string, request *aop.OpenSessionRequest) (*aop.OpenSessionResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil || s.newID == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(requestID) == "" {
		return rejectedOpen("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.applicationMu.Lock()
	defer s.applicationMu.Unlock()

	replayed := new(aop.OpenSessionResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "OpenSession", requestID, request, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %w", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedOpen("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.OpenSessionResponse) (*aop.OpenSessionResponse, error) {
		if err := FinishRequest(ctx, s.store, "OpenSession", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %w", err)
		}
		return response, nil
	}
	if strings.TrimSpace(request.NodeId) == "" {
		return finish(rejectedOpen("INVALID_ARGUMENT", "node_id is required"))
	}
	scanID, err := openSessionScanID(request)
	if err != nil {
		return finish(rejectedOpen("INVALID_ARGUMENT", err.Error()))
	}
	agentName, connected := s.runtime.AgentInfo(request.NodeId)
	if !connected {
		return finish(rejectedOpen("UNAVAILABLE", "node is not connected"))
	}

	id := strings.TrimSpace(request.SessionId)
	if id == "" {
		id = s.newID()
	}
	createdNew := false
	var created *types.SessionRecord
	if existing, err := s.store.GetSession(ctx, id); err == nil {
		if existing.GetSession().GetNodeId() != request.NodeId {
			return finish(rejectedOpen("ALREADY_EXISTS", "session is bound to another node"))
		}
		created = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get session: %w", err)
	} else {
		now := timestamppb.Now()
		created = &types.SessionRecord{
			Session:   &aop.Session{Id: id, State: SessionStateOpen, NodeId: request.NodeId, Title: request.Title},
			AgentName: agentName,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.store.CreateSession(ctx, created); err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
		createdNew = true
	}
	cleanup := func() {
		if createdNew {
			_ = s.store.DeleteSession(context.Background(), id)
		}
	}
	if scanID != "" {
		if _, err := s.runtime.GetScan(ctx, scanID); err != nil {
			cleanup()
			return finish(rejectedOpen("NOT_FOUND", "scan not found"))
		}
		if err := s.store.LinkScanToSession(ctx, id, scanID); err != nil {
			cleanup()
			return nil, fmt.Errorf("link scan to session: %w", err)
		}
	}
	forward := proto.Clone(request).(*aop.OpenSessionRequest)
	forward.SessionId = id
	if err := s.runtime.OpenAgentSession(ctx, requestID, forward); err != nil {
		cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return finish(rejectedOpen(string(ErrorCode(err)), err.Error()))
	}
	return finish(&aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Accepted{Accepted: created.GetSession()}})
}

func (s *Sessions) RunTurn(ctx context.Context, requestID string, request *aop.RunTurnRequest) (*aop.RunTurnResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil || s.newID == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(requestID) == "" {
		return rejectedRun("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.applicationMu.Lock()
	defer s.applicationMu.Unlock()

	replayed := new(aop.RunTurnResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "RunTurn", requestID, request, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %w", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedRun("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.RunTurnResponse) (*aop.RunTurnResponse, error) {
		if err := FinishRequest(ctx, s.store, "RunTurn", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %w", err)
		}
		return response, nil
	}
	if strings.TrimSpace(request.SessionId) == "" || (!request.ContinueSession && (request.Input == nil || len(request.Input.Content) == 0)) {
		return finish(rejectedRun("INVALID_ARGUMENT", "session_id and input.content are required unless continue_session is true"))
	}
	session, err := s.store.GetSession(ctx, request.SessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return finish(rejectedRun("NOT_FOUND", "session not found"))
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if _, connected := s.runtime.AgentInfo(session.GetSession().GetNodeId()); !connected {
		return finish(rejectedRun("UNAVAILABLE", "node is not connected"))
	}
	turnID := strings.TrimSpace(request.TurnId)
	if turnID == "" {
		turnID = s.newID()
	}
	session.UpdatedAt = timestamppb.Now()
	if session.GetSession().GetTitle() == "" && request.Input != nil {
		if session.Session == nil {
			session.Session = &aop.Session{}
		}
		session.Session.Title = contentText(request.Input.Content, 60)
	}
	_ = s.store.UpdateSession(ctx, session)

	forward := proto.Clone(request).(*aop.RunTurnRequest)
	if forward.Input == nil {
		forward.Input = &aop.Message{Role: "user"}
	}
	forward.TurnId = turnID
	response := &aop.RunTurnResponse{Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: request.SessionId,
		TurnId:    turnID,
		State:     "running",
	}}}
	if _, err := finish(response); err != nil {
		return nil, err
	}
	if !request.ContinueSession {
		s.runtime.PublishUserMessage(request.SessionId, turnID, forward.Input)
	}
	s.runtime.StartAgentTurn(request.SessionId, forward)
	return response, nil
}

func (s *Sessions) CancelTurn(ctx context.Context, requestID string, request *aop.CancelTurnRequest) (*aop.CancelTurnResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(requestID) == "" {
		return rejectedCancel("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.applicationMu.Lock()
	defer s.applicationMu.Unlock()

	replayed := new(aop.CancelTurnResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "CancelTurn", requestID, request, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %w", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedCancel("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.CancelTurnResponse) (*aop.CancelTurnResponse, error) {
		if err := FinishRequest(ctx, s.store, "CancelTurn", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %w", err)
		}
		return response, nil
	}
	if strings.TrimSpace(request.SessionId) == "" || strings.TrimSpace(request.TurnId) == "" {
		return finish(rejectedCancel("INVALID_ARGUMENT", "session_id and turn_id are required"))
	}
	if err := s.runtime.CancelAgentTurn(ctx, request.SessionId, request.TurnId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedCancel("NOT_FOUND", "session not found"))
		}
		if errors.Is(err, ErrTurnNotFound) {
			return finish(rejectedCancel("NOT_FOUND", "turn not found"))
		}
		return nil, fmt.Errorf("cancel turn: %w", err)
	}
	return finish(&aop.CancelTurnResponse{Outcome: &aop.CancelTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: request.SessionId,
		TurnId:    request.TurnId,
		State:     "canceled",
	}}})
}

func (s *Sessions) CloseSession(ctx context.Context, requestID string, request *aop.CloseSessionRequest) (*aop.CloseSessionResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(requestID) == "" {
		return rejectedClose("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.applicationMu.Lock()
	defer s.applicationMu.Unlock()

	replayed := new(aop.CloseSessionResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "CloseSession", requestID, request, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %w", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedClose("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.CloseSessionResponse) (*aop.CloseSessionResponse, error) {
		if err := FinishRequest(ctx, s.store, "CloseSession", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %w", err)
		}
		return response, nil
	}
	if strings.TrimSpace(request.SessionId) == "" {
		return finish(rejectedClose("INVALID_ARGUMENT", "session_id is required"))
	}
	session, err := s.store.GetSession(ctx, request.SessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return finish(rejectedClose("NOT_FOUND", "session not found"))
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	connected, err := s.runtime.CloseAgentSession(ctx, requestID, session.GetSession().GetNodeId(), proto.Clone(request).(*aop.CloseSessionRequest))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return finish(rejectedClose(string(ErrorCode(err)), err.Error()))
	}
	if session.Session == nil {
		session.Session = &aop.Session{}
	}
	session.Session.State = SessionStateClosed
	session.UpdatedAt = timestamppb.Now()
	if err := s.store.UpdateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("close session: %w", err)
	}
	if !connected {
		s.runtime.PublishSessionEvent(request.SessionId, &aop.Event{
			SessionId: request.SessionId,
			Emitter:   "aiscan.web",
			Payload:   &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: request.Reason}},
		})
	}
	return finish(&aop.CloseSessionResponse{Outcome: &aop.CloseSessionResponse_Accepted{Accepted: session.GetSession()}})
}

func (s *Sessions) ResetSession(ctx context.Context, request *types.ResetSessionRequest) (*types.ResetSessionResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil || s.newID == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.SessionId) == "" {
		return rejectedReset(request, "INVALID_ARGUMENT", "request_id and session_id are required"), nil
	}
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	replayed := new(types.ResetSessionResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "ResetSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, err
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedReset(request, "ALREADY_EXISTS", "request_id conflicts with another request"), nil
	}
	finish := func(response *types.ResetSessionResponse) (*types.ResetSessionResponse, error) {
		if err := FinishRequest(ctx, s.store, "ResetSession", request.RequestId, hash, response); err != nil {
			return nil, err
		}
		return response, nil
	}
	old, err := s.store.GetSession(ctx, request.SessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return finish(rejectedReset(request, "NOT_FOUND", "session not found"))
	}
	if err != nil {
		return nil, err
	}
	newID := strings.TrimSpace(request.NewSessionId)
	if newID == "" {
		newID = s.newID()
	}
	opened, err := s.OpenSession(ctx, request.RequestId+":open", &aop.OpenSessionRequest{SessionId: newID, NodeId: old.GetSession().GetNodeId(), Title: request.Title})
	if err != nil {
		return nil, err
	}
	if rejected := opened.GetRejected(); rejected != nil {
		return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	closed, err := s.CloseSession(ctx, request.RequestId+":close", &aop.CloseSessionRequest{SessionId: old.GetSession().GetId(), Reason: "reset"})
	if err != nil {
		_ = s.runtime.DeleteSession(context.Background(), newID)
		return nil, err
	}
	if rejected := closed.GetRejected(); rejected != nil {
		_ = s.runtime.DeleteSession(context.Background(), newID)
		return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	current, err := s.store.GetSession(ctx, newID)
	if err != nil {
		return nil, err
	}
	return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Accepted{Accepted: &types.ResetSessionReceipt{Previous: closed.GetAccepted(), Current: current}}})
}

func (s *Sessions) DeleteSession(ctx context.Context, request *types.DeleteSessionRequest) (*types.DeleteSessionResponse, error) {
	if s == nil || s.store == nil || s.runtime == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.SessionId) == "" {
		return rejectedDelete(request, "INVALID_ARGUMENT", "request_id and session_id are required"), nil
	}
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	replayed := new(types.DeleteSessionResponse)
	hash, found, conflict, err := BeginRequest(ctx, s.store, "DeleteSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, err
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedDelete(request, "ALREADY_EXISTS", "request_id conflicts with another request"), nil
	}
	finish := func(response *types.DeleteSessionResponse) (*types.DeleteSessionResponse, error) {
		if err := FinishRequest(ctx, s.store, "DeleteSession", request.RequestId, hash, response); err != nil {
			return nil, err
		}
		return response, nil
	}
	session, err := s.store.GetSession(ctx, request.SessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return finish(rejectedDelete(request, "NOT_FOUND", "session not found"))
	}
	if err != nil {
		return nil, err
	}
	if err := s.runtime.DeleteSession(ctx, request.SessionId); err != nil {
		return nil, err
	}
	return finish(&types.DeleteSessionResponse{RequestId: request.RequestId, Outcome: &types.DeleteSessionResponse_Accepted{Accepted: &aop.Session{Id: session.GetSession().GetId(), State: "deleted", NodeId: session.GetSession().GetNodeId(), Title: session.GetSession().GetTitle()}}})
}

func (s *Sessions) ListCommands(_ context.Context, request *types.ListCommandsRequest) (*types.ListCommandsResponse, error) {
	if s == nil || s.runtime == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.SessionId) == "" {
		return nil, Errorf(CodeInvalidArgument, "session_id is required")
	}
	return &types.ListCommandsResponse{Commands: cloneCommandSpecs(s.runtime.SessionMenu(request.SessionId))}, nil
}

func (s *Sessions) ListEvents(ctx context.Context, request *aop.ListEventsRequest) (*aop.ListEventsResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.SessionId) == "" {
		return nil, Errorf(CodeInvalidArgument, "session_id is required")
	}
	after, err := parseAOPCursor(request.AfterCursor)
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	stored, err := s.store.ListAOPEventsAfter(ctx, request.SessionId, after, int(request.Limit))
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	response := &aop.ListEventsResponse{Events: make([]*aop.EventDelivery, 0, len(stored))}
	for _, item := range stored {
		if item == nil || item.Event == nil {
			continue
		}
		response.Events = append(response.Events, item)
		response.NextCursor = item.Cursor
	}
	return response, nil
}

func (s *Sessions) WatchEvents(ctx context.Context, request *aop.WatchEventsRequest, send func(*aop.EventDelivery) error) error {
	if s == nil || s.store == nil || s.runtime == nil {
		return Errorf(CodeFailedPrecondition, "session service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.SessionId) == "" {
		return Errorf(CodeInvalidArgument, "session_id is required")
	}
	if send == nil {
		return Errorf(CodeInvalidArgument, "event sender is unavailable")
	}
	after, err := parseAOPCursor(request.AfterCursor)
	if err != nil {
		return NewError(CodeInvalidArgument, err)
	}
	live, unsubscribe := s.runtime.SubscribeSessionEvents(request.SessionId)
	defer unsubscribe()
	replayed, err := s.store.ListAOPEventsAfter(ctx, request.SessionId, after, 0)
	if err != nil {
		return fmt.Errorf("replay events: %w", err)
	}
	for _, item := range replayed {
		if item == nil || item.Event == nil {
			continue
		}
		cursor, err := parseAOPCursor(item.Cursor)
		if err != nil {
			return err
		}
		if err := send(item); err != nil {
			return err
		}
		if cursor > after {
			after = cursor
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-live:
			if !ok {
				return nil
			}
			if item == nil || item.Event == nil {
				continue
			}
			cursor, err := parseAOPCursor(item.Cursor)
			if err != nil {
				return err
			}
			if cursor > 0 && cursor <= after {
				continue
			}
			if err := send(item); err != nil {
				return err
			}
			if cursor > after {
				after = cursor
			}
		}
	}
}

func BeginRequest(ctx context.Context, store RequestJournal, method, requestID string, request, response proto.Message) ([]byte, bool, bool, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, false, false, err
	}
	digest := sha256.Sum256(raw)
	found, conflict, err := store.LoadAOPRequest(ctx, requestID, method, digest[:], response)
	return digest[:], found, conflict, err
}

func FinishRequest(ctx context.Context, store RequestJournal, method, requestID string, hash []byte, response proto.Message) error {
	return store.SaveAOPRequest(ctx, requestID, method, hash, response)
}

func cloneCommandSpecs(values []*types.CommandSpec) []*types.CommandSpec {
	result := make([]*types.CommandSpec, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, proto.Clone(value).(*types.CommandSpec))
		}
	}
	return result
}

func parseAOPCursor(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("invalid cursor %q", value)
	}
	return cursor, nil
}

func openSessionScanID(request *aop.OpenSessionRequest) (string, error) {
	if request == nil {
		return "", nil
	}
	for _, extension := range request.Extensions {
		link := new(types.SessionBinding)
		if extension == nil || !extension.MessageIs(link) {
			continue
		}
		if err := extension.UnmarshalTo(link); err != nil {
			return "", fmt.Errorf("decode scan extension: %w", err)
		}
		return strings.TrimSpace(link.ScanId), nil
	}
	return "", nil
}

func contentText(content []*aop.Content, limit int) string {
	var text strings.Builder
	for _, part := range content {
		value := part.GetText().GetText()
		if value == "" {
			continue
		}
		if text.Len() > 0 {
			text.WriteByte(' ')
		}
		text.WriteString(value)
	}
	value := strings.TrimSpace(text.String())
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func rejectedOpen(code, message string) *aop.OpenSessionResponse {
	return &aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Rejected{Rejected: rejection(code, message)}}
}

func rejectedRun(code, message string) *aop.RunTurnResponse {
	return &aop.RunTurnResponse{Outcome: &aop.RunTurnResponse_Rejected{Rejected: rejection(code, message)}}
}

func rejectedCancel(code, message string) *aop.CancelTurnResponse {
	return &aop.CancelTurnResponse{Outcome: &aop.CancelTurnResponse_Rejected{Rejected: rejection(code, message)}}
}

func rejectedClose(code, message string) *aop.CloseSessionResponse {
	return &aop.CloseSessionResponse{Outcome: &aop.CloseSessionResponse_Rejected{Rejected: rejection(code, message)}}
}

func rejectedReset(request *types.ResetSessionRequest, code, message string) *types.ResetSessionResponse {
	response := &types.ResetSessionResponse{Outcome: &types.ResetSessionResponse_Rejected{Rejected: &aop.Rejection{Code: code, Message: message}}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func rejectedDelete(request *types.DeleteSessionRequest, code, message string) *types.DeleteSessionResponse {
	response := &types.DeleteSessionResponse{Outcome: &types.DeleteSessionResponse_Rejected{Rejected: &aop.Rejection{Code: code, Message: message}}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}
