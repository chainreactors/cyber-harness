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
	chatpb "github.com/chainreactors/aiscan/aop/aiscan/chat"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxUploadSize = 50 << 20 // 50 MB

var ErrUploadTooLarge = errors.New("uploaded file exceeds the size limit")

type connectSessionServer struct {
	chatconnect.UnimplementedSessionServiceHandler
	service *Service
	chat    *aopChatServer
	mu      sync.Mutex
}

func newConnectSessionServer(service *Service, chat *aopChatServer) *connectSessionServer {
	return &connectSessionServer{service: service, chat: chat}
}

func (s *connectSessionServer) ListSessions(ctx context.Context, req *connect.Request[chatpb.ListSessionsRequest]) (*connect.Response[chatpb.ListSessionsResponse], error) {
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
	response := &chatpb.ListSessionsResponse{Sessions: make([]*chatpb.SessionRecord, 0, len(sessions))}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, sessionRecord(session))
	}
	if more {
		response.NextCursor = strconv.Itoa(offset + len(sessions))
	}
	return connect.NewResponse(response), nil
}

func (s *connectSessionServer) GetSession(ctx context.Context, req *connect.Request[chatpb.GetSessionRequest]) (*connect.Response[chatpb.GetSessionResponse], error) {
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
	return connect.NewResponse(&chatpb.GetSessionResponse{Session: sessionRecord(session)}), nil
}

func (s *connectSessionServer) ResetSession(ctx context.Context, req *connect.Request[chatpb.ResetSessionRequest]) (*connect.Response[chatpb.ResetSessionResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedReset(request, codes.InvalidArgument, "request_id is required")), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(chatpb.ResetSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "ResetSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedReset(request, codes.AlreadyExists, "request_id conflicts with another request")), nil
	}
	finish := func(response *chatpb.ResetSessionResponse) (*connect.Response[chatpb.ResetSessionResponse], error) {
		if err := s.finishRequest(ctx, "ResetSession", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	old, err := s.service.store.GetSession(ctx, request.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedReset(request, codes.NotFound, "session not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	newID := strings.TrimSpace(request.NewSessionId)
	if newID == "" {
		newID = generateID()
	}
	openResponse, err := s.chat.OpenSession(ctx, &aop.OpenSessionRequest{
		RequestId: request.RequestId + ":open", SessionId: newID, Participant: old.AgentID, Title: request.Title,
	})
	if err != nil {
		return nil, asConnectError(err)
	}
	if rejected := openResponse.GetRejected(); rejected != nil {
		return finish(&chatpb.ResetSessionResponse{RequestId: request.RequestId, Outcome: &chatpb.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	closeResponse, err := s.chat.CloseSession(ctx, &aop.CloseSessionRequest{
		RequestId: request.RequestId + ":close", SessionId: old.ID, Reason: "reset",
	})
	if err != nil {
		_ = s.service.DeleteSession(context.Background(), newID)
		return nil, asConnectError(err)
	}
	if rejected := closeResponse.GetRejected(); rejected != nil {
		_ = s.service.DeleteSession(context.Background(), newID)
		return finish(&chatpb.ResetSessionResponse{RequestId: request.RequestId, Outcome: &chatpb.ResetSessionResponse_Rejected{Rejected: rejected}})
	}
	current, err := s.service.store.GetSession(ctx, newID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return finish(&chatpb.ResetSessionResponse{RequestId: request.RequestId, Outcome: &chatpb.ResetSessionResponse_Accepted{Accepted: &chatpb.ResetSessionReceipt{
		Previous: closeResponse.GetAccepted(), Current: sessionRecord(current),
	}}})
}

func (s *connectSessionServer) DeleteSession(ctx context.Context, req *connect.Request[chatpb.DeleteSessionRequest]) (*connect.Response[chatpb.DeleteSessionResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedDelete(request, codes.InvalidArgument, "request_id is required")), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(chatpb.DeleteSessionResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "DeleteSession", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedDelete(request, codes.AlreadyExists, "request_id conflicts with another request")), nil
	}
	finish := func(response *chatpb.DeleteSessionResponse) (*connect.Response[chatpb.DeleteSessionResponse], error) {
		if err := s.finishRequest(ctx, "DeleteSession", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	session, err := s.service.store.GetSession(ctx, request.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finish(rejectedDelete(request, codes.NotFound, "session not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.service.DeleteSession(ctx, request.SessionId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return finish(&chatpb.DeleteSessionResponse{RequestId: request.RequestId, Outcome: &chatpb.DeleteSessionResponse_Accepted{Accepted: &aop.Session{
		Id: session.ID, State: "deleted", Participant: session.AgentID, Title: session.Title,
	}}})
}

func (s *connectSessionServer) ListCommands(_ context.Context, req *connect.Request[chatpb.ListCommandsRequest]) (*connect.Response[chatpb.ListCommandsResponse], error) {
	if req.Msg == nil || strings.TrimSpace(req.Msg.SessionId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id is required"))
	}
	specs := s.service.SessionMenu(req.Msg.SessionId)
	response := &chatpb.ListCommandsResponse{Commands: make([]*chatpb.CommandSpec, 0, len(specs))}
	for _, spec := range specs {
		response.Commands = append(response.Commands, &chatpb.CommandSpec{Name: spec.Name, Aliases: spec.Aliases, Usage: spec.Usage, Description: spec.Description})
	}
	return connect.NewResponse(response), nil
}

func (s *connectSessionServer) ExecuteCommand(ctx context.Context, req *connect.Request[chatpb.ExecuteCommandRequest]) (*connect.Response[chatpb.ExecuteCommandResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedCommand(request, codes.InvalidArgument, "request_id is required")), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(chatpb.ExecuteCommandResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "ExecuteCommand", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedCommand(request, codes.AlreadyExists, "request_id conflicts with another request")), nil
	}
	finish := func(response *chatpb.ExecuteCommandResponse) (*connect.Response[chatpb.ExecuteCommandResponse], error) {
		if err := s.finishRequest(ctx, "ExecuteCommand", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	operationID, err := s.service.ExecuteSessionCommand(request.SessionId, request.Line)
	if err != nil {
		code := codes.FailedPrecondition
		if errors.Is(err, ErrSessionNotFound) {
			code = codes.NotFound
		}
		return finish(rejectedCommand(request, code, err.Error()))
	}
	return finish(&chatpb.ExecuteCommandResponse{RequestId: request.RequestId, Outcome: &chatpb.ExecuteCommandResponse_Accepted{Accepted: &chatpb.CommandReceipt{
		OperationId: operationID, SessionId: request.SessionId, State: "running",
	}}})
}

func (s *connectSessionServer) UploadSessionFile(ctx context.Context, req *connect.Request[chatpb.UploadSessionFileRequest]) (*connect.Response[chatpb.UploadSessionFileResponse], error) {
	request := req.Msg
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return connect.NewResponse(rejectedUpload(request, codes.InvalidArgument, "request_id is required")), nil
	}
	if len(request.Data) > maxUploadSize {
		return connect.NewResponse(rejectedUpload(request, codes.ResourceExhausted, ErrUploadTooLarge.Error())), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replayed := new(chatpb.UploadSessionFileResponse)
	hash, found, conflict, err := s.beginRequest(ctx, "UploadSessionFile", request.RequestId, request, replayed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if found {
		return connect.NewResponse(replayed), nil
	}
	if conflict {
		return connect.NewResponse(rejectedUpload(request, codes.AlreadyExists, "request_id conflicts with another request")), nil
	}
	finish := func(response *chatpb.UploadSessionFileResponse) (*connect.Response[chatpb.UploadSessionFileResponse], error) {
		if err := s.finishRequest(ctx, "UploadSessionFile", request.RequestId, hash, response); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(response), nil
	}
	result, err := s.service.HandleFileUpload(ctx, request.SessionId, request.Filename, request.Data)
	if err != nil {
		code := codes.FailedPrecondition
		if errors.Is(err, ErrSessionNotFound) {
			code = codes.NotFound
		}
		return finish(rejectedUpload(request, code, err.Error()))
	}
	mediaType := request.MediaType
	return finish(&chatpb.UploadSessionFileResponse{RequestId: request.RequestId, Outcome: &chatpb.UploadSessionFileResponse_Accepted{Accepted: &chatpb.UploadedFile{
		Filename: result.Filename, Path: result.Path, Size: int64(result.Size), MediaType: mediaType,
	}}})
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

func sessionRecord(session *ChatSession) *chatpb.SessionRecord {
	if session == nil {
		return nil
	}
	state := "open"
	if session.Status != SessionActive {
		state = "closed"
	}
	return &chatpb.SessionRecord{
		Session:   &aop.Session{Id: session.ID, State: state, Participant: session.AgentID, Title: session.Title},
		AgentName: session.AgentName, ScanIds: append([]string(nil), session.ScanIDs...),
		CreatedAt: timestamppb.New(session.CreatedAt), UpdatedAt: timestamppb.New(session.UpdatedAt),
	}
}

func rejectedReset(req *chatpb.ResetSessionRequest, code codes.Code, message string) *chatpb.ResetSessionResponse {
	response := &chatpb.ResetSessionResponse{Outcome: &chatpb.ResetSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedDelete(req *chatpb.DeleteSessionRequest, code codes.Code, message string) *chatpb.DeleteSessionResponse {
	response := &chatpb.DeleteSessionResponse{Outcome: &chatpb.DeleteSessionResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedCommand(req *chatpb.ExecuteCommandRequest, code codes.Code, message string) *chatpb.ExecuteCommandResponse {
	response := &chatpb.ExecuteCommandResponse{Outcome: &chatpb.ExecuteCommandResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}

func rejectedUpload(req *chatpb.UploadSessionFileRequest, code codes.Code, message string) *chatpb.UploadSessionFileResponse {
	response := &chatpb.UploadSessionFileResponse{Outcome: &chatpb.UploadSessionFileResponse_Rejected{Rejected: rejection(code, message)}}
	if req != nil {
		response.RequestId = req.RequestId
	}
	return response
}
