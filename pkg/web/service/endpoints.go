package service

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	web "github.com/chainreactors/aiscan/pkg/web"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
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

// webSocketEnvelopeStream adapts a gorilla WebSocket to the transport-neutral
// aop.EnvelopeStream; writes are serialized.
type webSocketEnvelopeStream struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *webSocketEnvelopeStream) Recv() (*aop.Envelope, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	envelope := new(aop.Envelope)
	if err := protobuf.Unmarshal(data, envelope); err != nil {
		return nil, fmt.Errorf("decode AOP envelope: %w", err)
	}
	return envelope, nil
}

func (s *webSocketEnvelopeStream) Send(envelope *aop.Envelope) error {
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}
