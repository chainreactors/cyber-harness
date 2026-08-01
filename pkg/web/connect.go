package web

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
	"github.com/chainreactors/aiscan/aop/aiscan/scan/scanconnect"
	"github.com/chainreactors/aiscan/aop/aopconnect"
	"github.com/chainreactors/aiscan/pkg/web/auth"
	"google.golang.org/grpc/status"
)

// Protobuf JSON base64-encodes bytes, so a 50 MiB UploadSessionFile payload can
// occupy roughly 67 MiB on the wire. Leave enough envelope headroom while the
// business method continues to enforce the exact 50 MiB file limit.
const connectMaxMessageBytes = 72 << 20

// NewConnectHandler exposes the public AOP service and AIScan's product-specific
// chat service from the same protobuf schemas. Generated Connect handlers also
// accept native gRPC and gRPC-Web requests on the canonical procedure paths.
func NewConnectHandler(accessKey string, service *Service) http.Handler {
	interceptor := connectAuthInterceptor{accessKey: accessKey}
	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptor),
		connect.WithReadMaxBytes(connectMaxMessageBytes),
		connect.WithSendMaxBytes(connectMaxMessageBytes),
	}
	mux := http.NewServeMux()
	chatCore := NewAOPChatServer(service).(*aopChatServer)
	chatPath, chatHandler := aopconnect.NewChatServiceHandler(&connectChatServer{core: chatCore}, opts...)
	sessionPath, sessionHandler := chatconnect.NewSessionServiceHandler(newConnectSessionServer(service, chatCore), opts...)
	scanPath, scanHandler := scanconnect.NewScanServiceHandler(newConnectScanServer(service), opts...)
	mux.Handle(chatPath, chatHandler)
	mux.Handle(sessionPath, sessionHandler)
	mux.Handle(scanPath, scanHandler)
	return mux
}

type connectChatServer struct {
	aopconnect.UnimplementedChatServiceHandler
	core *aopChatServer
}

func (s *connectChatServer) OpenSession(ctx context.Context, req *connect.Request[aop.OpenSessionRequest]) (*connect.Response[aop.OpenSessionResponse], error) {
	response, err := s.core.OpenSession(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectChatServer) RunTurn(ctx context.Context, req *connect.Request[aop.RunTurnRequest]) (*connect.Response[aop.RunTurnResponse], error) {
	response, err := s.core.RunTurn(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectChatServer) CancelTurn(ctx context.Context, req *connect.Request[aop.CancelTurnRequest]) (*connect.Response[aop.CancelTurnResponse], error) {
	response, err := s.core.CancelTurn(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectChatServer) CloseSession(ctx context.Context, req *connect.Request[aop.CloseSessionRequest]) (*connect.Response[aop.CloseSessionResponse], error) {
	response, err := s.core.CloseSession(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectChatServer) ListEvents(ctx context.Context, req *connect.Request[aop.ListEventsRequest]) (*connect.Response[aop.ListEventsResponse], error) {
	response, err := s.core.ListEvents(ctx, req.Msg)
	return connectResponse(response, err)
}

func (s *connectChatServer) WatchEvents(ctx context.Context, req *connect.Request[aop.WatchEventsRequest], stream *connect.ServerStream[aop.WatchEventsResponse]) error {
	return asConnectError(s.core.watchEvents(req.Msg, ctx, stream.Send))
}

func connectResponse[T any](response *T, err error) (*connect.Response[T], error) {
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
	if grpcStatus, ok := status.FromError(err); ok {
		return connect.NewError(connect.Code(grpcStatus.Code()), errors.New(grpcStatus.Message()))
	}
	return connect.NewError(connect.CodeInternal, err)
}

type connectAuthInterceptor struct {
	accessKey string
}

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

var _ aopconnect.ChatServiceHandler = (*connectChatServer)(nil)
var _ chatconnect.SessionServiceHandler = (*connectSessionServer)(nil)
