package web

import (
	"net/http"

	aop "github.com/chainreactors/aiscan/aop"
)

// HandleAOPWebSocket is the only application WebSocket entrypoint. The first
// protobuf Envelope selects the concrete peer role: AgentHello enters the
// runner loop; every other supported request enters the browser application
// loop. Both roles use the same envelope and namespace messages.
func HandleAOPWebSocket(service *Service, agents *AgentPool, w http.ResponseWriter, r *http.Request) {
	if service == nil || agents == nil {
		http.Error(w, "AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := agents.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stream := &webSocketEnvelopeStream{conn: conn}
	first, err := stream.Recv()
	if err != nil {
		return
	}
	message, err := aop.Unwrap(first)
	if err != nil {
		return
	}
	if core, ok := message.(*aop.ProtocolMessage); ok && core.GetAgentHello() != nil {
		_ = agents.serveAgentStream(r.Context(), stream, first)
		return
	}
	_ = service.serveBrowserAOP(r.Context(), stream, first)
}
