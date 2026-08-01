package agent

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
)

type disconnectChatHandler struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (h *disconnectChatHandler) OpenSession(context.Context, *aop.OpenSessionRequest) *aop.OpenSessionResponse {
	return nil
}
func (h *disconnectChatHandler) RunTurn(ctx context.Context, request *aop.RunTurnRequest) *aop.RunTurnResponse {
	h.once.Do(func() { close(h.started) })
	go func() { <-ctx.Done(); close(h.canceled) }()
	return &aop.RunTurnResponse{RequestId: request.RequestId, Outcome: &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{SessionId: request.SessionId, TurnId: request.TurnId}}}
}
func (*disconnectChatHandler) CancelTurn(*aop.CancelTurnRequest) *aop.CancelTurnResponse { return nil }
func (*disconnectChatHandler) CloseSession(context.Context, *aop.CloseSessionRequest) *aop.CloseSessionResponse {
	return nil
}
func (*disconnectChatHandler) Command(context.Context, *transport.CommandRequest) (*transport.CommandResult, error) {
	return nil, fmt.Errorf("unused")
}
func (*disconnectChatHandler) Upload(*transport.FileUploadRequest) (*transport.FileResult, error) {
	return nil, fmt.Errorf("unused")
}
func (*disconnectChatHandler) ReloadConfig(string) (*transport.ConfigReloadResult, *transport.AgentStatus) {
	return nil, nil
}

type disconnectStream struct {
	ctx     context.Context
	handler *disconnectChatHandler
	index   int
}

func (s *disconnectStream) Context() context.Context       { return s.ctx }
func (*disconnectStream) Send(*transport.AgentFrame) error { return nil }
func (s *disconnectStream) Recv() (*transport.ServerFrame, error) {
	s.index++
	switch s.index {
	case 1:
		return &transport.ServerFrame{Payload: &transport.ServerFrame_Accepted{Accepted: &transport.ConnectionAccepted{AgentId: "worker"}}}, nil
	case 2:
		return &transport.ServerFrame{CorrelationId: "turn-1", Payload: &transport.ServerFrame_RunTurn{RunTurn: &aop.RunTurnRequest{RequestId: "turn-1", SessionId: "chat-1", TurnId: "turn-1", Input: &aop.Message{Role: "user"}}}}, nil
	default:
		select {
		case <-s.handler.started:
			return nil, io.EOF
		case <-time.After(time.Second):
			return nil, fmt.Errorf("chat handler did not start")
		}
	}
}

func TestAgentConnectionCancelsChatWhenStreamDisconnects(t *testing.T) {
	handler := &disconnectChatHandler{started: make(chan struct{}), canceled: make(chan struct{})}
	err := serveAgentConnection(context.Background(), connectionConfig{Name: "worker", Registry: commands.NewRegistry(), Chat: handler, Node: protocols.NodeRef{ID: "worker", Authority: "local"}}, telemetry.NopLogger(), &disconnectStream{ctx: context.Background(), handler: handler})
	if err == nil {
		t.Fatal("connection returned nil after disconnect")
	}
	select {
	case <-handler.canceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("chat context remained alive after disconnect")
	}
}
