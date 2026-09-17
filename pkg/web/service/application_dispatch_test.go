package service

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	protobuf "google.golang.org/protobuf/proto"
)

type applicationTestStream struct {
	mu       sync.Mutex
	received []*aop.Envelope
	sent     []*aop.Envelope
}

func (s *applicationTestStream) Recv() (*aop.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.received) == 0 {
		return nil, io.EOF
	}
	envelope := s.received[0]
	s.received = s.received[1:]
	return envelope, nil
}

func (s *applicationTestStream) Send(envelope *aop.Envelope) error {
	s.mu.Lock()
	s.sent = append(s.sent, envelope)
	s.mu.Unlock()
	return nil
}

func TestSessionOnlyApplicationCanAddItsOwnNamespace(t *testing.T) {
	first, err := aop.Wrap("request", "", &toolpb.ProtocolMessage{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	svc := NewService(ServiceConfig{RegisterApplicationNamespaces: func(mux *aop.NamespaceMux) error {
		return mux.Register(&toolpb.ProtocolMessage{}, func(context.Context, *aop.Envelope, protobuf.Message, aop.SendFunc) error {
			called = true
			return nil
		})
	}})
	stream := &applicationTestStream{received: []*aop.Envelope{first}}
	if err := svc.ServeApplication(t.Context(), stream); !errors.Is(err, io.EOF) {
		t.Fatalf("ServeApplication() error = %v, want EOF", err)
	}
	if !called {
		t.Fatal("session-only application did not dispatch its extension")
	}

	svc.applicationNamespaces = func(mux *aop.NamespaceMux) error {
		return mux.Register(&aop.ProtocolMessage{}, func(context.Context, *aop.Envelope, protobuf.Message, aop.SendFunc) error { return nil })
	}
	rejected := &applicationTestStream{received: []*aop.Envelope{first}}
	if err := svc.ServeApplication(t.Context(), rejected); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("ServeApplication() error = %v, want duplicate namespace error", err)
	}
}
