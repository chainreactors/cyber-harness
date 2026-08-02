package web

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"github.com/chainreactors/aiscan/pkg/web/auth"
)

// Protobuf JSON base64-encodes SCO import bytes, so the 50 MiB business limit
// needs roughly 67 MiB on the management wire.
const connectMaxMessageBytes = 72 << 20

// connectServer is the single adapter for all generated RPC services.
// Management calls enter pkg/web/api; the AOP bidi stream enters the same
// Envelope service core used by the browser WebSocket.
type connectServer struct {
	api     *managementapi.API
	service *Service
}

// NewConnectHandler exposes management RPCs and the AOP bidirectional stream
// over Connect and gRPC. The browser keeps its compatible WebSocket transport.
func NewConnectHandler(accessKey string, service *Service) http.Handler {
	interceptor := connectAuthInterceptor{accessKey: accessKey}
	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptor),
		connect.WithReadMaxBytes(connectMaxMessageBytes),
		connect.WithSendMaxBytes(connectMaxMessageBytes),
	}
	server := &connectServer{api: service.api, service: service}
	mux := http.NewServeMux()
	register := func(path string, handler http.Handler) { mux.Handle(path, handler) }
	path, handler := rpc.NewAOPServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewSessionServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewScanServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewConfigServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewAgentServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewSystemServiceHandler(server, opts...)
	register(path, handler)
	path, handler = rpc.NewSCOServiceHandler(server, opts...)
	register(path, handler)
	return mux
}

func mountConnectHandlers(mux *http.ServeMux, handler http.Handler) {
	for _, service := range []string{
		rpc.AOPServiceName,
		rpc.SessionServiceName,
		rpc.ScanServiceName,
		rpc.ConfigServiceName,
		rpc.AgentServiceName,
		rpc.SystemServiceName,
		rpc.SCOServiceName,
	} {
		mux.Handle("/"+service+"/", handler)
	}
}

type connectEnvelopeStream struct {
	stream *connect.BidiStream[aop.Envelope, aop.Envelope]
}

func (s connectEnvelopeStream) Recv() (*aop.Envelope, error) { return s.stream.Receive() }
func (s connectEnvelopeStream) Send(envelope *aop.Envelope) error {
	return s.stream.Send(envelope)
}

func (s *connectServer) Connect(ctx context.Context, stream *connect.BidiStream[aop.Envelope, aop.Envelope]) error {
	err := s.service.serveAOPStream(ctx, connectEnvelopeStream{stream: stream})
	if errors.Is(err, io.EOF) {
		return nil
	}
	return asConnectError(err)
}

func (s *connectServer) ListSessions(ctx context.Context, req *connect.Request[types.ListSessionsRequest]) (*connect.Response[types.ListSessionsResponse], error) {
	return connectCall(s.api.Sessions.ListSessions(ctx, req.Msg))
}

func (s *connectServer) GetSession(ctx context.Context, req *connect.Request[types.GetSessionRequest]) (*connect.Response[types.GetSessionResponse], error) {
	return connectCall(s.api.Sessions.GetSession(ctx, req.Msg))
}

func (s *connectServer) ResetSession(ctx context.Context, req *connect.Request[types.ResetSessionRequest]) (*connect.Response[types.ResetSessionResponse], error) {
	return connectCall(s.api.Sessions.ResetSession(ctx, req.Msg))
}

func (s *connectServer) DeleteSession(ctx context.Context, req *connect.Request[types.DeleteSessionRequest]) (*connect.Response[types.DeleteSessionResponse], error) {
	return connectCall(s.api.Sessions.DeleteSession(ctx, req.Msg))
}

func (s *connectServer) ListCommands(ctx context.Context, req *connect.Request[types.ListCommandsRequest]) (*connect.Response[types.ListCommandsResponse], error) {
	return connectCall(s.api.Sessions.ListCommands(ctx, req.Msg))
}

func (s *connectServer) ListEvents(ctx context.Context, req *connect.Request[aop.ListEventsRequest]) (*connect.Response[aop.ListEventsResponse], error) {
	return connectCall(s.api.Sessions.ListEvents(ctx, req.Msg))
}

func (s *connectServer) SubmitScan(ctx context.Context, req *connect.Request[types.SubmitScanRequest]) (*connect.Response[types.SubmitScanResponse], error) {
	return connectCall(s.api.Scans.SubmitScan(ctx, req.Msg))
}

func (s *connectServer) GetScan(ctx context.Context, req *connect.Request[types.GetScanRequest]) (*connect.Response[types.GetScanResponse], error) {
	return connectCall(s.api.Scans.GetScan(ctx, req.Msg))
}

func (s *connectServer) ListScans(ctx context.Context, req *connect.Request[types.ListScansRequest]) (*connect.Response[types.ListScansResponse], error) {
	return connectCall(s.api.Scans.ListScans(ctx, req.Msg))
}

func (s *connectServer) CancelScan(ctx context.Context, req *connect.Request[types.CancelScanRequest]) (*connect.Response[types.CancelScanResponse], error) {
	return connectCall(s.api.Scans.CancelScan(ctx, req.Msg))
}

func (s *connectServer) GetScanReport(ctx context.Context, req *connect.Request[types.GetScanReportRequest]) (*connect.Response[types.GetScanReportResponse], error) {
	return connectCall(s.api.Scans.GetScanReport(ctx, req.Msg))
}

func (s *connectServer) GetConfig(ctx context.Context, req *connect.Request[types.GetConfigRequest]) (*connect.Response[types.GetConfigResponse], error) {
	return connectCall(s.api.Config.GetConfig(ctx, req.Msg))
}

func (s *connectServer) UpdateConfig(ctx context.Context, req *connect.Request[types.UpdateConfigRequest]) (*connect.Response[types.UpdateConfigResponse], error) {
	return connectCall(s.api.Config.UpdateConfig(ctx, req.Msg))
}

func (s *connectServer) ActivateProfile(ctx context.Context, req *connect.Request[types.ActivateProfileRequest]) (*connect.Response[types.ActivateProfileResponse], error) {
	return connectCall(s.api.Config.ActivateProfile(ctx, req.Msg))
}

func (s *connectServer) TestLLM(ctx context.Context, req *connect.Request[types.LLMProbeRequest]) (*connect.Response[types.LLMProbeResult], error) {
	return connectCall(s.api.Config.TestLLM(ctx, req.Msg))
}

func (s *connectServer) ListModels(ctx context.Context, req *connect.Request[types.LLMProbeRequest]) (*connect.Response[types.ListModelsResult], error) {
	return connectCall(s.api.Config.ListModels(ctx, req.Msg))
}

func (s *connectServer) TestConnection(ctx context.Context, req *connect.Request[types.TestConnectionRequest]) (*connect.Response[types.TestConnectionResponse], error) {
	return connectCall(s.api.Config.TestConnection(ctx, req.Msg))
}

func (s *connectServer) ListAgents(_ context.Context, req *connect.Request[types.ListAgentsRequest]) (*connect.Response[types.ListAgentsResponse], error) {
	return connect.NewResponse(s.api.ListAgents(req.Msg)), nil
}

func (s *connectServer) GetStatus(_ context.Context, req *connect.Request[types.GetStatusRequest]) (*connect.Response[types.GetStatusResponse], error) {
	return connect.NewResponse(s.api.GetStatus(req.Msg)), nil
}

func (s *connectServer) ListNodes(ctx context.Context, req *connect.Request[types.ListNodesRequest]) (*connect.Response[types.ListNodesResponse], error) {
	return connectCall(s.api.SCO.ListNodes(ctx, req.Msg))
}

func (s *connectServer) GetNode(ctx context.Context, req *connect.Request[types.GetNodeRequest]) (*connect.Response[types.GetNodeResponse], error) {
	return connectCall(s.api.SCO.GetNode(ctx, req.Msg))
}

func (s *connectServer) GetStats(ctx context.Context, req *connect.Request[types.GetStatsRequest]) (*connect.Response[types.GetStatsResponse], error) {
	return connectCall(s.api.SCO.GetStats(ctx, req.Msg))
}

func (s *connectServer) DeleteNodes(ctx context.Context, req *connect.Request[types.DeleteNodesRequest]) (*connect.Response[types.DeleteNodesResponse], error) {
	return connectCall(s.api.SCO.DeleteNodes(ctx, req.Msg))
}

func (s *connectServer) ImportNodes(ctx context.Context, req *connect.Request[types.ImportNodesRequest]) (*connect.Response[types.ImportNodesResponse], error) {
	return connectCall(s.api.SCO.ImportNodes(ctx, req.Msg))
}

func (s *connectServer) ListArtifacts(ctx context.Context, req *connect.Request[types.ListArtifactsRequest]) (*connect.Response[types.ListArtifactsResponse], error) {
	return connectCall(s.api.SCO.ListArtifacts(ctx, req.Msg))
}

func connectCall[T any](response *T, err error) (*connect.Response[T], error) {
	if err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(response), nil
}

func asConnectError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	code := connect.CodeInternal
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	default:
		switch managementapi.ErrorCode(err) {
		case managementapi.CodeInvalidArgument:
			code = connect.CodeInvalidArgument
		case managementapi.CodeNotFound:
			code = connect.CodeNotFound
		case managementapi.CodeAlreadyExists:
			code = connect.CodeAlreadyExists
		case managementapi.CodeFailedPrecondition:
			code = connect.CodeFailedPrecondition
		case managementapi.CodeResourceExhausted:
			code = connect.CodeResourceExhausted
		case managementapi.CodeUnavailable:
			code = connect.CodeUnavailable
		}
	}
	return connect.NewError(code, err)
}

type connectAuthInterceptor struct{ accessKey string }

func (i connectAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if !connectAuthenticated(req.Header(), i.accessKey) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access key"))
		}
		return next(ctx, req)
	}
}

func (i connectAuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i connectAuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if !connectAuthenticated(conn.RequestHeader(), i.accessKey) {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access key"))
		}
		return next(ctx, conn)
	}
}

func connectAuthenticated(header http.Header, accessKey string) bool {
	if accessKey == "" {
		return true
	}
	if token, ok := auth.BearerToken(header.Get("Authorization")); ok {
		return auth.AccessKeyMatches(accessKey, token)
	}
	request := &http.Request{Header: header}
	if cookie, err := request.Cookie(auth.CookieName); err == nil {
		return auth.SessionMatches(accessKey, cookie.Value)
	}
	return false
}

var (
	_ rpc.AOPServiceHandler     = (*connectServer)(nil)
	_ rpc.SessionServiceHandler = (*connectServer)(nil)
	_ rpc.ScanServiceHandler    = (*connectServer)(nil)
	_ rpc.ConfigServiceHandler  = (*connectServer)(nil)
	_ rpc.AgentServiceHandler   = (*connectServer)(nil)
	_ rpc.SystemServiceHandler  = (*connectServer)(nil)
	_ rpc.SCOServiceHandler     = (*connectServer)(nil)
)
