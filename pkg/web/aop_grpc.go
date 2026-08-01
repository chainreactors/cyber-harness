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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type aopChatServer struct {
	aop.UnimplementedChatServiceServer
	service *Service
	mu      sync.Mutex
}

const agentControlTimeout = 10 * time.Second

func NewAOPChatServer(service *Service) aop.ChatServiceServer {
	return &aopChatServer{service: service}
}

func (s *aopChatServer) OpenSession(ctx context.Context, req *aop.OpenSessionRequest) (*aop.OpenSessionResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, status.Error(codes.FailedPrecondition, "chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(req.RequestId) == "" {
		return rejectedOpen(req, codes.InvalidArgument, "request_id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.OpenSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "OpenSession", req.RequestId, req, replayed)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedOpen(req, codes.AlreadyExists, "request_id conflicts with another request"), nil
	}
	finish := func(response *aop.OpenSessionResponse) (*aop.OpenSessionResponse, error) {
		if err := s.finishRequest(ctx, "OpenSession", req.RequestId, hash, response); err != nil {
			return nil, status.Errorf(codes.Internal, "save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.Participant) == "" {
		return finish(rejectedOpen(req, codes.InvalidArgument, "participant is required"))
	}
	if s.service.agents == nil || s.service.agents.get(req.Participant) == nil {
		return finish(rejectedOpen(req, codes.Unavailable, "participant is not connected"))
	}

	id := strings.TrimSpace(req.SessionId)
	if id == "" {
		id = generateID()
	}
	createdNew := false
	var created *ChatSession
	if existing, err := s.service.store.GetSession(ctx, id); err == nil {
		if existing.AgentID != req.Participant {
			return finish(rejectedOpen(req, codes.AlreadyExists, "session is bound to another participant"))
		}
		created = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, status.Errorf(codes.Internal, "get session: %v", err)
	} else {
		now := time.Now()
		created = &ChatSession{
			ID: id, AgentID: req.Participant, Title: req.Title, Status: SessionActive,
			CreatedAt: now, UpdatedAt: now,
		}
		if agent := s.service.agents.get(req.Participant); agent != nil {
			created.AgentName = agent.name
		}
		if err := s.service.store.CreateSession(ctx, created); err != nil {
			return nil, status.Errorf(codes.Internal, "create session: %v", err)
		}
		createdNew = true
	}
	if !s.service.agents.SessionOpen(req.Participant, id) {
		forward := proto.Clone(req).(*aop.OpenSessionRequest)
		forward.SessionId = id
		resultCh, err := s.service.agents.DispatchOpenSession(req.Participant, forward)
		if err != nil {
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return finish(rejectedOpen(req, codes.Unavailable, err.Error()))
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
					message = "participant disconnected while opening session"
				}
				return finish(rejectedOpen(req, codes.FailedPrecondition, message))
			}
		case <-ctx.Done():
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
			if createdNew {
				_ = s.service.store.DeleteSession(context.Background(), id)
			}
			return finish(rejectedOpen(req, codes.Unavailable, "participant timed out while opening session"))
		}
	}
	return finish(&aop.OpenSessionResponse{RequestId: req.RequestId, Outcome: &aop.OpenSessionResponse_Accepted{Accepted: sessionToAOP(created)}})
}

func (s *aopChatServer) RunTurn(ctx context.Context, req *aop.RunTurnRequest) (*aop.RunTurnResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, status.Error(codes.FailedPrecondition, "chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(req.RequestId) == "" {
		return rejectedRun(req, codes.InvalidArgument, "request_id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.RunTurnResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "RunTurn", req.RequestId, req, replayed)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedRun(req, codes.AlreadyExists, "request_id conflicts with another request"), nil
	}
	finish := func(response *aop.RunTurnResponse) (*aop.RunTurnResponse, error) {
		if err := s.finishRequest(ctx, "RunTurn", req.RequestId, hash, response); err != nil {
			return nil, status.Errorf(codes.Internal, "save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" || (!req.ContinueSession && (req.Input == nil || len(req.Input.Content) == 0)) {
		return finish(rejectedRun(req, codes.InvalidArgument, "session_id and input.content are required unless continue_session is true"))
	}
	session, err := s.service.store.GetSession(ctx, req.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedRun(req, codes.NotFound, "session not found"))
		}
		return nil, status.Errorf(codes.Internal, "get session: %v", err)
	}
	if s.service.sessionAgent(req.SessionId) == nil {
		return finish(rejectedRun(req, codes.Unavailable, "participant is not connected"))
	}
	turnID := strings.TrimSpace(req.TurnId)
	if turnID == "" {
		turnID = generateID()
	}
	session.UpdatedAt = time.Now()
	if session.Title == "" {
		session.Title = contentText(req.Input.Content, 60)
	}
	_ = s.service.store.UpdateSession(ctx, session)

	forward := *req
	if forward.Input == nil {
		forward.Input = &aop.Message{Role: "user"}
	}
	forward.TurnId = turnID
	response := &aop.RunTurnResponse{RequestId: req.RequestId, Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: req.SessionId, TurnId: turnID, State: "running",
	}}}
	if _, err := finish(response); err != nil {
		return nil, err
	}
	s.service.handleAgentRun(req.SessionId, &forward)
	return response, nil
}

func (s *aopChatServer) CancelTurn(ctx context.Context, req *aop.CancelTurnRequest) (*aop.CancelTurnResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, status.Error(codes.FailedPrecondition, "chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(req.RequestId) == "" {
		return rejectedCancel(req, codes.InvalidArgument, "request_id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.CancelTurnResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "CancelTurn", req.RequestId, req, replayed)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedCancel(req, codes.AlreadyExists, "request_id conflicts with another request"), nil
	}
	finish := func(response *aop.CancelTurnResponse) (*aop.CancelTurnResponse, error) {
		if err := s.finishRequest(ctx, "CancelTurn", req.RequestId, hash, response); err != nil {
			return nil, status.Errorf(codes.Internal, "save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" || strings.TrimSpace(req.TurnId) == "" {
		return finish(rejectedCancel(req, codes.InvalidArgument, "session_id and turn_id are required"))
	}
	if err := s.service.CancelTurn(ctx, req.SessionId, req.TurnId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedCancel(req, codes.NotFound, "session not found"))
		}
		if errors.Is(err, ErrTurnNotFound) {
			return finish(rejectedCancel(req, codes.NotFound, "turn not found"))
		}
		return nil, status.Errorf(codes.Internal, "cancel turn: %v", err)
	}
	return finish(&aop.CancelTurnResponse{RequestId: req.RequestId, Outcome: &aop.CancelTurnResponse_Accepted{Accepted: &aop.TurnReceipt{
		SessionId: req.SessionId, TurnId: req.TurnId, State: "canceled",
	}}})
}

func (s *aopChatServer) CloseSession(ctx context.Context, req *aop.CloseSessionRequest) (*aop.CloseSessionResponse, error) {
	if s.service == nil || s.service.store == nil {
		return nil, status.Error(codes.FailedPrecondition, "chat service is unavailable")
	}
	if req == nil || strings.TrimSpace(req.RequestId) == "" {
		return rejectedClose(req, codes.InvalidArgument, "request_id is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(aop.CloseSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "CloseSession", req.RequestId, req, replayed)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load request journal: %v", err)
	}
	if found {
		return replayed, nil
	}
	if conflict {
		return rejectedClose(req, codes.AlreadyExists, "request_id conflicts with another request"), nil
	}
	finish := func(response *aop.CloseSessionResponse) (*aop.CloseSessionResponse, error) {
		if err := s.finishRequest(ctx, "CloseSession", req.RequestId, hash, response); err != nil {
			return nil, status.Errorf(codes.Internal, "save request journal: %v", err)
		}
		return response, nil
	}
	if strings.TrimSpace(req.SessionId) == "" {
		return finish(rejectedClose(req, codes.InvalidArgument, "session_id is required"))
	}
	session, err := s.service.store.GetSession(ctx, req.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedClose(req, codes.NotFound, "session not found"))
		}
		return nil, status.Errorf(codes.Internal, "get session: %v", err)
	}
	agentConnected := s.service.agents != nil && s.service.agents.get(session.AgentID) != nil
	if agentConnected {
		resultCh, dispatchErr := s.service.agents.DispatchCloseSession(session.AgentID, proto.Clone(req).(*aop.CloseSessionRequest))
		if dispatchErr != nil {
			return finish(rejectedClose(req, codes.Unavailable, dispatchErr.Error()))
		}
		timer := time.NewTimer(agentControlTimeout)
		defer timer.Stop()
		select {
		case result, ok := <-resultCh:
			if !ok || result.Err != "" {
				message := result.Err
				if message == "" {
					message = "participant disconnected while closing session"
				}
				return finish(rejectedClose(req, codes.FailedPrecondition, message))
			}
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
			return finish(rejectedClose(req, codes.Unavailable, "participant timed out while closing session"))
		}
	}
	session.Status = SessionArchived
	session.UpdatedAt = time.Now()
	if err := s.service.store.UpdateSession(ctx, session); err != nil {
		return nil, status.Errorf(codes.Internal, "close session: %v", err)
	}
	if !agentConnected {
		s.service.BroadcastAOPEvent(req.SessionId, &aop.Event{
			SessionId: req.SessionId, Emitter: "aiscan.web",
			Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: req.Reason}},
		})
	}
	return finish(&aop.CloseSessionResponse{RequestId: req.RequestId, Outcome: &aop.CloseSessionResponse_Accepted{Accepted: sessionToAOP(session)}})
}

func (s *aopChatServer) ListEvents(ctx context.Context, req *aop.ListEventsRequest) (*aop.ListEventsResponse, error) {
	if s.service == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	after, err := parseAOPCursor(req.AfterCursor)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	stored, err := s.service.store.ListAOPEventsAfter(ctx, req.SessionId, after, int(req.Limit))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list events: %v", err)
	}
	response := &aop.ListEventsResponse{Events: make([]*aop.EventDelivery, 0, len(stored))}
	for _, item := range stored {
		response.Events = append(response.Events, delivery(item.Cursor, item.Event))
		response.NextCursor = strconv.FormatInt(item.Cursor, 10)
	}
	return response, nil
}

func (s *aopChatServer) WatchEvents(req *aop.WatchEventsRequest, stream aop.ChatService_WatchEventsServer) error {
	return s.watchEvents(req, stream.Context(), func(response *aop.WatchEventsResponse) error {
		return stream.Send(response)
	})
}

func (s *aopChatServer) watchEvents(req *aop.WatchEventsRequest, ctx context.Context, send func(*aop.WatchEventsResponse) error) error {
	if s.service == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		return status.Error(codes.InvalidArgument, "session_id is required")
	}
	if send == nil {
		return status.Error(codes.Internal, "event sender is unavailable")
	}
	after, err := parseAOPCursor(req.AfterCursor)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	live, unsubscribe := s.service.hub.SubscribeAOP(req.SessionId)
	defer unsubscribe()
	replayed, err := s.service.store.ListAOPEventsAfter(ctx, req.SessionId, after, 0)
	if err != nil {
		return status.Errorf(codes.Internal, "replay events: %v", err)
	}
	for _, item := range replayed {
		if err := send(&aop.WatchEventsResponse{Delivery: delivery(item.Cursor, item.Event)}); err != nil {
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
			if err := send(&aop.WatchEventsResponse{Delivery: delivery(item.Cursor, item.Event)}); err != nil {
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

func sessionToAOP(session *ChatSession) *aop.Session {
	if session == nil {
		return nil
	}
	state := "open"
	if session.Status != SessionActive {
		state = "closed"
	}
	return &aop.Session{Id: session.ID, State: state, Participant: session.AgentID, Title: session.Title}
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

func rejection(code codes.Code, message string) *aop.Rejection {
	return &aop.Rejection{Code: canonicalCode(code), Message: message}
}

func canonicalCode(code codes.Code) string {
	switch code {
	case codes.InvalidArgument:
		return "INVALID_ARGUMENT"
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.FailedPrecondition:
		return "FAILED_PRECONDITION"
	case codes.Unavailable:
		return "UNAVAILABLE"
	case codes.ResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case codes.Unauthenticated:
		return "UNAUTHENTICATED"
	case codes.Internal:
		return "INTERNAL"
	default:
		return strings.ToUpper(strings.ReplaceAll(code.String(), " ", "_"))
	}
}

func rejectedOpen(req *aop.OpenSessionRequest, code codes.Code, message string) *aop.OpenSessionResponse {
	response := &aop.OpenSessionResponse{Outcome: &aop.OpenSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedRun(req *aop.RunTurnRequest, code codes.Code, message string) *aop.RunTurnResponse {
	response := &aop.RunTurnResponse{Outcome: &aop.RunTurnResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedCancel(req *aop.CancelTurnRequest, code codes.Code, message string) *aop.CancelTurnResponse {
	response := &aop.CancelTurnResponse{Outcome: &aop.CancelTurnResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedClose(req *aop.CloseSessionRequest, code codes.Code, message string) *aop.CloseSessionResponse {
	response := &aop.CloseSessionResponse{Outcome: &aop.CloseSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}
