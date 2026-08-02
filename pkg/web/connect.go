package web

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	"github.com/chainreactors/aiscan/pkg/web/auth"
)

// Protobuf JSON base64-encodes SCO import bytes, so the 50 MiB business limit
// needs roughly 67 MiB on the management wire.
const connectMaxMessageBytes = 72 << 20

// NewConnectHandler exposes AIScan's management/query services from their
// protobuf schemas. Realtime AOP, file, command and PTY traffic is not mounted
// here; it uses the single application WebSocket.
func NewConnectHandler(accessKey string, service *Service, pool *AgentPool, local *LocalAgents) http.Handler {
	interceptor := connectAuthInterceptor{accessKey: accessKey}
	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptor),
		connect.WithReadMaxBytes(connectMaxMessageBytes),
		connect.WithSendMaxBytes(connectMaxMessageBytes),
	}
	mux := http.NewServeMux()
	chatCore := NewAOPChatServer(service)
	sessionPath, sessionHandler := rpc.NewSessionServiceHandler(newConnectSessionServer(service, chatCore), opts...)
	scanPath, scanHandler := rpc.NewScanServiceHandler(newConnectScanServer(service), opts...)
	configPath, configHandler := rpc.NewConfigServiceHandler(newConnectConfigServer(service), opts...)
	agentPath, agentHandler := rpc.NewAgentServiceHandler(&connectAgentServer{pool: pool, local: local}, opts...)
	systemPath, systemHandler := rpc.NewSystemServiceHandler(&connectSystemServer{service: service, pool: pool, serverURL: "/"}, opts...)
	scoPath, scoHandler := rpc.NewSCOServiceHandler(&connectSCOServer{service: service}, opts...)
	mux.Handle(sessionPath, sessionHandler)
	mux.Handle(scanPath, scanHandler)
	mux.Handle(configPath, configHandler)
	mux.Handle(agentPath, agentHandler)
	mux.Handle(systemPath, systemHandler)
	mux.Handle(scoPath, scoHandler)
	return mux
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

var _ rpc.SessionServiceHandler = (*connectSessionServer)(nil)
