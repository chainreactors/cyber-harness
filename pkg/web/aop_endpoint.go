package web

import (
	"context"
	"fmt"
	"net/http"

	aop "github.com/chainreactors/aiscan/aop"
)

// HandleAOPWebSocket is the only application WebSocket entrypoint. The first
// protobuf Envelope selects the concrete peer role: AgentHello enters the
// runner loop; every other supported request enters the browser application
// loop. Both roles use the same envelope and namespace messages.
func HandleAOPWebSocket(service *Service, w http.ResponseWriter, r *http.Request) {
	if service == nil || service.agents == nil {
		http.Error(w, "AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := service.agents.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stream := &webSocketEnvelopeStream{conn: conn}
	_ = service.serveAOPStream(r.Context(), stream)
}

func (s *Service) serveAOPStream(ctx context.Context, stream aop.EnvelopeStream) error {
	if s == nil || s.agents == nil || stream == nil {
		return fmt.Errorf("AOP stream is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	message, err := aop.Unwrap(first)
	if err != nil {
		return err
	}
	if core, ok := message.(*aop.ProtocolMessage); ok && core.GetAgentHello() != nil {
		return s.agents.serveAgentStream(ctx, stream, first)
	}
	return s.serveBrowserAOP(ctx, stream, first)
}
