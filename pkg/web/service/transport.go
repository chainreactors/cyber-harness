package service

import (
	"fmt"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

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
