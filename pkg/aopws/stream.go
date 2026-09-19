// Package aopws adapts Gorilla WebSocket connections to AOP envelope streams.
package aopws

import (
	"context"
	"fmt"
	"sync"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
)

type Encoding uint8

const (
	Binary Encoding = iota
	ProtoJSON
)

type Options struct {
	Encoding     Encoding
	WriteTimeout time.Duration
	PingInterval time.Duration
	PongTimeout  time.Duration
}

// Stream owns WebSocket framing, liveness and serialized writes for one AOP
// connection. The caller retains responsibility for dialing or upgrading it.
type Stream struct {
	conn         *websocket.Conn
	encoding     Encoding
	writeTimeout time.Duration
	pingInterval time.Duration

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

func New(ctx context.Context, conn *websocket.Conn, options Options) (*Stream, error) {
	if conn == nil {
		return nil, fmt.Errorf("AOP WebSocket connection is required")
	}
	if options.Encoding != Binary && options.Encoding != ProtoJSON {
		return nil, fmt.Errorf("unsupported AOP WebSocket encoding %d", options.Encoding)
	}
	if options.WriteTimeout < 0 || options.PingInterval < 0 || options.PongTimeout < 0 {
		return nil, fmt.Errorf("AOP WebSocket timeouts cannot be negative")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stream := &Stream{
		conn:         conn,
		encoding:     options.Encoding,
		writeTimeout: options.WriteTimeout,
		pingInterval: options.PingInterval,
		done:         make(chan struct{}),
	}
	if options.PongTimeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(options.PongTimeout)); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(options.PongTimeout))
		})
	}
	go stream.monitor(ctx)
	return stream, nil
}

func (s *Stream) Recv() (*aop.Envelope, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	envelope := new(aop.Envelope)
	switch s.encoding {
	case Binary:
		err = protobuf.Unmarshal(data, envelope)
	case ProtoJSON:
		err = protojson.Unmarshal(data, envelope)
	}
	if err != nil {
		return nil, fmt.Errorf("decode AOP envelope: %w", err)
	}
	return envelope, nil
}

func (s *Stream) Send(envelope *aop.Envelope) error {
	if envelope == nil {
		return fmt.Errorf("AOP envelope is required")
	}
	frame := websocket.BinaryMessage
	var data []byte
	var err error
	switch s.encoding {
	case Binary:
		data, err = protobuf.Marshal(envelope)
	case ProtoJSON:
		frame = websocket.TextMessage
		data, err = protojson.Marshal(envelope)
	}
	if err != nil {
		return fmt.Errorf("encode AOP envelope: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return fmt.Errorf("AOP WebSocket is closed")
	default:
	}
	if s.writeTimeout > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return err
		}
	}
	return s.conn.WriteMessage(frame, data)
}

func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.conn.Close()
	})
	return err
}

func (s *Stream) monitor(ctx context.Context) {
	if s.pingInterval <= 0 {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
		return
	}
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Close()
			return
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.ping(); err != nil {
				_ = s.Close()
				return
			}
		}
	}
}

func (s *Stream) ping() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return fmt.Errorf("AOP WebSocket is closed")
	default:
	}
	deadline := time.Time{}
	if s.writeTimeout > 0 {
		deadline = time.Now().Add(s.writeTimeout)
	}
	return s.conn.WriteControl(websocket.PingMessage, nil, deadline)
}

var _ aop.EnvelopeStream = (*Stream)(nil)
