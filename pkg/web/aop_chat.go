package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
)

type aopChatServer struct {
	service *Service
	mu      sync.Mutex
}

const agentControlTimeout = 10 * time.Second

func NewAOPChatServer(service *Service) *aopChatServer {
	return &aopChatServer{service: service}
}

func (s *aopChatServer) OpenSession(ctx context.Context, requestID string, req *aop.OpenSessionRequest) (*aop.OpenSessionResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, fmt.Errorf("chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(requestID) == "" {
		return rejectedOpen("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.OpenSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "OpenSession", requestID, req, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedOpen("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.OpenSessionResponse) (*aop.OpenSessionResponse, error) {
		if err := s.finishRequest(ctx, "OpenSession", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.NodeId) == "" {
		return finish(rejectedOpen("INVALID_ARGUMENT", "node_id is required"))
	}
	scanID, err := openSessionScanID(req)
	if err != nil {
		return finish(rejectedOpen("INVALID_ARGUMENT", err.Error()))
	}
	if s.service.agents == nil || s.service.agents.get(req.NodeId) == nil {
		return finish(rejectedOpen("UNAVAILABLE", "node is not connected"))
	}

	id := strings.TrimSpace(req.SessionId)
	if id == "" {
		id = generateID()
	}
	createdNew := false
	var created *types.SessionRecord
	if existing, err := s.service.store.GetSession(ctx, id); err == nil {
		if existing.GetSession().GetNodeId() != req.NodeId {
			return finish(rejectedOpen("ALREADY_EXISTS", "session is bound to another node"))
		}
		created = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get session: %v", err)
	} else {
		now := nowProto()
		created = &types.SessionRecord{
			Session:   &aop.Session{Id: id, State: SessionStateOpen, NodeId: req.NodeId, Title: req.Title},
			CreatedAt: now, UpdatedAt: now,
		}
		if agent := s.service.agents.get(req.NodeId); agent != nil {
			created.AgentName = agent.Name()
		}
		if err := s.service.store.CreateSession(ctx, created); err != nil {
			return nil, fmt.Errorf("create session: %v", err)
		}
		createdNew = true
	}
	if scanID != "" {
		if _, err := s.service.store.Get(ctx, scanID); err != nil {
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return finish(rejectedOpen("NOT_FOUND", "scan not found"))
		}
		if err := s.service.store.LinkScanToSession(ctx, id, scanID); err != nil {
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return nil, fmt.Errorf("link scan to session: %v", err)
		}
	}
	if !s.service.agents.SessionOpen(req.NodeId, id) {
		forward := proto.Clone(req).(*aop.OpenSessionRequest)
		forward.SessionId = id
		resultCh, err := s.service.agents.DispatchOpenSession(req.NodeId, requestID, forward)
		if err != nil {
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return finish(rejectedOpen("UNAVAILABLE", err.Error()))
		}
		timer := time.NewTimer(agentControlTimeout)
		defer timer.Stop()
		select {
		case result, ok := <-resultCh:
			if !ok || result.Err != "" {
				if createdNew {
					_ = s.service.store.DeleteSession(context.Background(), id)
				}
				message := result.Err
				if message == "" {
					message = "node disconnected while opening session"
				}
				return finish(rejectedOpen("FAILED_PRECONDITION", message))
			}
		case <-ctx.Done():
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return nil, ctx.Err()
		case <-timer.C:
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return finish(rejectedOpen("UNAVAILABLE", "node timed out while opening session"))
		}
	}
	return finish(&aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Accepted{Accepted: created.GetSession()}})
}

func (s *aopChatServer) RunTurn(ctx context.Context, requestID string, req *aop.RunTurnRequest) (*aop.RunTurnResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, fmt.Errorf("chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(requestID) == "" {
		return rejectedRun("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.RunTurnResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "RunTurn", requestID, req, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedRun("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.RunTurnResponse) (*aop.RunTurnResponse, error) {
		if err := s.finishRequest(ctx, "RunTurn", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" || (!req.ContinueSession && (req.Input == nil || len(req.Input.Content) == 0)) {
		return finish(rejectedRun("INVALID_ARGUMENT", "session_id and input.content are required unless continue_session is true"))
	}
	session, err := s.service.store.GetSession(ctx, req.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedRun("NOT_FOUND", "session not found"))
		}
		return nil, fmt.Errorf("get session: %v", err)
	}
	if s.service.sessionAgent(req.SessionId) == nil {
		return finish(rejectedRun("UNAVAILABLE", "node is not connected"))
	}
	turnID := strings.TrimSpace(req.TurnId)
	if turnID == "" {
		turnID = generateID()
	}
	session.UpdatedAt = nowProto()
	if session.GetSession().GetTitle() == "" {
		if session.Session == nil {
			session.Session = &aop.Session{}
		}
		session.Session.Title = contentText(req.Input.Content, 60)
	}
	_ = s.service.store.UpdateSession(ctx, session)

	forward := *req
	if forward.Input == nil {
		forward.Input = &aop.Message{Role: "user"}
	}
	forward.TurnId = turnID
	response := &aop.RunTurnResponse{Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: req.SessionId, TurnId: turnID, State: "running",
	}}}
	if _, err := finish(response); err != nil {
		return nil, err
	}
	if !req.ContinueSession {
		s.service.publishUserMessage(req.SessionId, turnID, forward.Input)
	}
	s.service.handleAgentRun(req.SessionId, &forward)
	return response, nil
}

func (s *aopChatServer) CancelTurn(ctx context.Context, requestID string, req *aop.CancelTurnRequest) (*aop.CancelTurnResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, fmt.Errorf("chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(requestID) == "" {
		return rejectedCancel("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.CancelTurnResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "CancelTurn", requestID, req, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedCancel("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.CancelTurnResponse) (*aop.CancelTurnResponse, error) {
		if err := s.finishRequest(ctx, "CancelTurn", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" || strings.TrimSpace(req.TurnId) == "" {
		return finish(rejectedCancel("INVALID_ARGUMENT", "session_id and turn_id are required"))
	}
	if err := s.service.CancelTurn(ctx, req.SessionId, req.TurnId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedCancel("NOT_FOUND", "session not found"))
		}
		if errors.Is(err, ErrTurnNotFound) {
			return finish(rejectedCancel("NOT_FOUND", "turn not found"))
		}
		return nil, fmt.Errorf("cancel turn: %v", err)
	}
	return finish(&aop.CancelTurnResponse{Outcome: &aop.CancelTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: req.SessionId, TurnId: req.TurnId, State: "canceled",
	}}})
}

func (s *aopChatServer) CloseSession(ctx context.Context, requestID string, req *aop.CloseSessionRequest) (*aop.CloseSessionResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, fmt.Errorf("chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(requestID) == "" {
		return rejectedClose("INVALID_ARGUMENT", "envelope id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.CloseSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "CloseSession", requestID, req, replayed)
	if err != nil {
		return nil, fmt.Errorf("load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedClose("ALREADY_EXISTS", "envelope id conflicts with another request"), nil
	}
	finish := func(response *aop.CloseSessionResponse) (*aop.CloseSessionResponse, error) {
		if err := s.finishRequest(ctx, "CloseSession", requestID, hash, response); err != nil {
			return nil, fmt.Errorf("save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" {
		return finish(rejectedClose("INVALID_ARGUMENT", "session_id is required"))
	}
	session, err := s.service.store.GetSession(ctx, req.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedClose("NOT_FOUND", "session not found"))
		}
		return nil, fmt.Errorf("get session: %v", err)
	}
	agentConnected := s.service.agents != nil && s.service.agents.get(session.GetSession().GetNodeId()) != nil
	if agentConnected {
		resultCh, dispatchErr := s.service.agents.DispatchCloseSession(session.GetSession().GetNodeId(), requestID, proto.Clone(req).(*aop.CloseSessionRequest))
		if dispatchErr != nil {
			return finish(rejectedClose("UNAVAILABLE", dispatchErr.Error()))
		}
		timer := time.NewTimer(agentControlTimeout)
		defer timer.Stop()
		select {
		case result, ok := <-resultCh:
			if !ok || result.Err != "" {
				message := result.Err
				if message == "" {
					message = "node disconnected while closing session"
				}
				return finish(rejectedClose("FAILED_PRECONDITION", message))
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return finish(rejectedClose("UNAVAILABLE", "node timed out while closing session"))
		}
	}
	if session.Session == nil {
		session.Session = &aop.Session{}
	}
	session.Session.State = SessionStateClosed
	session.UpdatedAt = nowProto()
	if err := s.service.store.UpdateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("close session: %v", err)
	}
	if !agentConnected {
		s.service.BroadcastAOPEvent(req.SessionId, &aop.Event{
			SessionId: req.SessionId, Emitter: "aiscan.web",
			Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: req.Reason}},
		})
	}
	return finish(&aop.CloseSessionResponse{Outcome: &aop.CloseSessionResponse_Accepted{Accepted: session.GetSession()}})
}

func (s *aopChatServer) ListEvents(ctx context.Context, req *aop.ListEventsRequest) (*aop.ListEventsResponse, error) {
	if s.service == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	after, err := parseAOPCursor(req.AfterCursor)
	if err != nil {
		return nil, err
	}
	stored, err := s.service.store.ListAOPEventsAfter(ctx, req.SessionId, after, int(req.Limit))
	if err != nil {
		return nil, fmt.Errorf("list events: %v", err)
	}
	response := &aop.ListEventsResponse{Events: make([]*aop.EventDelivery, 0, len(stored))}
	for _, item := range stored {
		response.Events = append(response.Events, delivery(item.Cursor, item.Event))
		response.NextCursor = strconv.FormatInt(item.Cursor, 10)
	}
	return response, nil
}

func (s *aopChatServer) watchEvents(req *aop.WatchEventsRequest, ctx context.Context, send func(*aop.EventDelivery) error) error {
	if s.service == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		return fmt.Errorf("session_id is required")
	}
	if send == nil {
		return fmt.Errorf("event sender is unavailable")
	}
	after, err := parseAOPCursor(req.AfterCursor)
	if err != nil {
		return err
	}
	live, unsubscribe := s.service.hub.SubscribeAOP(req.SessionId)
	defer unsubscribe()
	replayed, err := s.service.store.ListAOPEventsAfter(ctx, req.SessionId, after, 0)
	if err != nil {
		return fmt.Errorf("replay events: %v", err)
	}
	for _, item := range replayed {
		if err := send(delivery(item.Cursor, item.Event)); err != nil {
			return err
		}
		if item.Cursor > after {
			after = item.Cursor
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
			if item.Event == nil || (item.Cursor > 0 && item.Cursor <= after) {
				continue
			}
			if err := send(delivery(item.Cursor, item.Event)); err != nil {
				return err
			}
			if item.Cursor > after {
				after = item.Cursor
			}
		}
	}
}

func (s *aopChatServer) beginRequest(ctx context.Context, method, requestID string, request, response proto.Message) (hash []byte, found, conflict bool, err error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, false, false, err
	}
	digest := sha256.Sum256(raw)
	found, conflict, err = s.service.store.LoadAOPRequest(ctx, requestID, method, digest[:], response)
	return digest[:], found, conflict, err
}

func (s *aopChatServer) finishRequest(ctx context.Context, method, requestID string, hash []byte, response proto.Message) error {
	return s.service.store.SaveAOPRequest(ctx, requestID, method, hash, response)
}

func delivery(cursor int64, event *aop.Event) *aop.EventDelivery {
	value := ""
	if cursor > 0 {
		value = strconv.FormatInt(cursor, 10)
	}
	return &aop.EventDelivery{Cursor: value, Event: event}
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

func rejection(code, message string) *aop.Rejection {
	return &aop.Rejection{Code: code, Message: message}
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
