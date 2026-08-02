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

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
)

type connectSessionServer struct {
	rpc.UnimplementedSessionServiceHandler
	service *Service
	chat    *aopChatServer
	mu      sync.Mutex
}

func newConnectSessionServer(service *Service, chat *aopChatServer) *connectSessionServer {
	return &connectSessionServer{service: service, chat: chat}
}

func (s *connectSessionServer) ListSessions(ctx context.Context, req *connect.Request[types.ListSessionsRequest]) (*connect.Response[types.ListSessionsResponse], error) {
	if s.service == nil || s.service.store == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("chat service is unavailable"))
	}
	offset := 0
	if value := strings.TrimSpace(req.Msg.AfterCursor); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid after_cursor %q", value))
		}
		offset = parsed
	}
	limit := int(req.Msg.Limit)
	if limit == 0 {
		limit = 100
	}
	sessions, more, err := s.service.store.ListSessionPage(ctx, offset, limit, req.Msg.IncludeClosed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &types.ListSessionsResponse{Sessions: sessions}
	if more {
		response.NextCursor = strconv.Itoa(offset + len(sessions))
	}
	return connect.NewResponse(response), nil
}

func (s *connectSessionServer) GetSession(ctx context.Context, req *connect.Request[types.GetSessionRequest]) (*connect.Response[types.GetSessionResponse], error) {
	if req.Msg == nil || strings.TrimSpace(req.Msg.SessionId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	session, err := s.service.store.GetSession(ctx, req.Msg.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&types.GetSessionResponse{Session: session}), nil
}

func (s *connectSessionServer) ResetSession(ctx context.Context, req *connect.Request[types.ResetSessionRequest]) (*connect.Response[types.ResetSessionResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedReset(request, "INVALID_ARGUMENT", "request_id is required")), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(types.ResetSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "ResetSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedReset(request, "ALREADY_EXISTS", "request_id conflicts with another request")), nil
	}
	finish := func(response *types.ResetSessionResponse) (*connect.Response[types.ResetSessionResponse], error) {
		if err := s.finishRequest(ctx, "ResetSession", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	old, err := s.service.store.GetSession(ctx, request.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedReset(request, "NOT_FOUND", "session not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	newID := strings.TrimSpace(request.NewSessionId)
	if newID == "" {
		newID = generateID()
	}
	openResponse, err := s.chat.OpenSession(ctx, request.RequestId+":open", &aop.OpenSessionRequest{
		SessionId: newID, NodeId: old.GetSession().GetNodeId(), Title: request.Title,
	})
	if err != nil {
		return nil, asConnectError(err)
	}
	if rejected := openResponse.GetRejected(); rejected != nil {
		return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	closeResponse, err := s.chat.CloseSession(ctx, request.RequestId+":close", &aop.CloseSessionRequest{
		SessionId: old.GetSession().GetId(), Reason: "reset",
	})
	if err != nil {
		_ = s.service.DeleteSession(context.Background(), newID)
		return nil, asConnectError(err)
	}
	if rejected := closeResponse.GetRejected(); rejected != nil {
		_ = s.service.DeleteSession(context.Background(), newID)
		return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	current, err := s.service.store.GetSession(ctx, newID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return finish(&types.ResetSessionResponse{RequestId: request.RequestId, Outcome: &types.ResetSessionResponse_Accepted{Accepted: &types.ResetSessionReceipt{
		Previous: closeResponse.GetAccepted(), Current: current,
	}}})
}

func (s *connectSessionServer) DeleteSession(ctx context.Context, req *connect.Request[types.DeleteSessionRequest]) (*connect.Response[types.DeleteSessionResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedDelete(request, "INVALID_ARGUMENT", "request_id is required")), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(types.DeleteSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "DeleteSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedDelete(request, "ALREADY_EXISTS", "request_id conflicts with another request")), nil
	}
	finish := func(response *types.DeleteSessionResponse) (*connect.Response[types.DeleteSessionResponse], error) {
		if err := s.finishRequest(ctx, "DeleteSession", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	session, err := s.service.store.GetSession(ctx, request.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedDelete(request, "NOT_FOUND", "session not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.service.DeleteSession(ctx, request.SessionId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return finish(&types.DeleteSessionResponse{RequestId: request.RequestId, Outcome: &types.DeleteSessionResponse_Accepted{Accepted: &aop.Session{
		Id: session.GetSession().GetId(), State: "deleted", NodeId: session.GetSession().GetNodeId(), Title: session.GetSession().GetTitle(),
	}}})
}

func (s *connectSessionServer) ListCommands(_ context.Context, req *connect.Request[types.ListCommandsRequest]) (*connect.Response[types.ListCommandsResponse], error) {
	if req.Msg == nil || strings.TrimSpace(req.Msg.SessionId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	specs := s.service.SessionMenu(req.Msg.SessionId)
	return connect.NewResponse(&types.ListCommandsResponse{Commands: cloneCommandSpecs(specs)}), nil
}

func (s *connectSessionServer) ListEvents(ctx context.Context, req *connect.Request[aop.ListEventsRequest]) (*connect.Response[aop.ListEventsResponse], error) {
	response, err := s.chat.ListEvents(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectSessionServer) beginRequest(ctx context.Context, method, requestID string, request, response proto.Message) ([]byte, bool, bool, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, false, false, err
	}
	digest := sha256.Sum256(raw)
	found, conflict, err := s.service.store.LoadAOPRequest(ctx, requestID, method, digest[:], response)
	return digest[:], found, conflict, err
}

func (s *connectSessionServer) finishRequest(ctx context.Context, method, requestID string, hash []byte, response proto.Message) error {
	return s.service.store.SaveAOPRequest(ctx, requestID, method, hash, response)
}

func rejectedReset(req *types.ResetSessionRequest, code, message string) *types.ResetSessionResponse {
	response := &types.ResetSessionResponse{Outcome: &types.ResetSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedDelete(req *types.DeleteSessionRequest, code, message string) *types.DeleteSessionResponse {
	response := &types.DeleteSessionResponse{Outcome: &types.DeleteSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}
