package service

import (
	"context"
	"fmt"
	"net/http"

	aop "github.com/chainreactors/aiscan/aop"
	web "github.com/chainreactors/aiscan/pkg/web"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"github.com/gorilla/websocket"
)

const (
	ApplicationWebSocketPath = web.ApplicationWebSocketPath
	NodeWebSocketPath        = web.NodeWebSocketPath
)

func serveEnvelopeWebSocket(upgrader websocket.Upgrader, serve func(context.Context, aop.EnvelopeStream) error, w http.ResponseWriter, r *http.Request) {
	if serve == nil {
		http.Error(w, "AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = serve(r.Context(), &webSocketEnvelopeStream{conn: conn})
}

func (s *Service) HandleApplicationWebSocket(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.agents == nil {
		http.Error(w, "application AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	serveEnvelopeWebSocket(s.agents.upgrader, s.ServeApplication, w, r)
}

func (s *Service) ApplicationWebSocketHandler() http.Handler {
	if s == nil || s.agents == nil {
		return nil
	}
	return http.HandlerFunc(s.HandleApplicationWebSocket)
}

func (s *Service) NodeWebSocketHandler() http.Handler {
	if s == nil || s.agents == nil {
		return nil
	}
	return http.HandlerFunc(s.agents.HandleNodeWebSocket)
}

func (p *AgentPool) HandleNodeWebSocket(w http.ResponseWriter, r *http.Request) {
	if p == nil {
		http.Error(w, "node AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	serveEnvelopeWebSocket(p.upgrader, p.ServeNode, w, r)
}

// ServeApplication performs the Application Endpoint initialization and then
// hands the unified Connection to the api business dispatcher.
func (s *Service) ServeApplication(ctx context.Context, stream aop.EnvelopeStream) error {
	if s == nil || s.api == nil || stream == nil {
		return fmt.Errorf("application AOP stream is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	connection, err := web.NewConnection(ctx, stream)
	if err != nil {
		return err
	}
	defer connection.Close()

	if message, unwrapErr := aop.Unwrap(first); unwrapErr == nil {
		if core, ok := message.(*aop.ProtocolMessage); ok && core.GetAgentHello() != nil {
			protocolErr, wrapErr := aop.Wrap(generateID(), first.GetId(), &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{
				Code: "WRONG_ENDPOINT", Message: "AgentHello is only accepted by the node endpoint",
			}}})
			if wrapErr == nil {
				_ = connection.Send(protocolErr)
			}
			return fmt.Errorf("AgentHello sent to application endpoint")
		}
	}

	backends := &managementapi.ApplicationBackends{
		Sessions: s.api.Sessions,
		Scans:    s.api.Scans,
		Commands: s,
		Files:    s,
		NewID:    generateID,
	}
	if s.agents != nil {
		backends.PTY = ptyRouter{pool: s.agents}
	}
	return managementapi.ServeApplication(connection, first, backends)
}
