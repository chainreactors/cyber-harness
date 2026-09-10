package runtime

import (
	"context"
	"fmt"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

func RuntimeCommandSpecs() []*types.CommandSpec {
	return []*types.CommandSpec{
		{Name: "/status", Description: "Show Agent LLM, tool, scanner, and session health"},
		{Name: "/clear", Description: "Clear the current Agent context"},
		{Name: "/compact", Usage: "/compact [focus]", Description: "Compact the current Agent context"},
	}
}

func (rt *AgentRuntime) OpenAOPSession(req *aop.OpenSessionRequest) *aop.OpenSessionResponse {
	response := &aop.OpenSessionResponse{}
	if rt == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		response.Outcome = &aop.OpenSessionResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id is required")}
		return response
	}
	session, err := rt.EnsureSession(SessionOptions{ID: req.SessionId, ParentSessionID: req.ParentSessionId, ParentToolCallID: req.ParentToolCallId})
	if err != nil {
		response.Outcome = &aop.OpenSessionResponse_Rejected{Rejected: rejection("FAILED_PRECONDITION", err.Error())}
		return response
	}
	response.Outcome = &aop.OpenSessionResponse_Accepted{Accepted: &aop.Session{Id: session.ID(), State: "open", NodeId: req.NodeId, Title: req.Title}}
	return response
}

func (rt *AgentRuntime) RunAOPTurn(ctx context.Context, req *aop.RunTurnRequest) *aop.RunTurnResponse {
	response := &aop.RunTurnResponse{}
	if rt == nil || req == nil || (!req.ContinueSession && req.Input == nil) || strings.TrimSpace(req.SessionId) == "" || strings.TrimSpace(req.TurnId) == "" {
		response.Outcome = &aop.RunTurnResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id, turn_id, and input are required unless continue_session is true")}
		return response
	}
	options := new(types.AgentRunOptions)
	for _, extension := range req.Extensions {
		if extension != nil && extension.MessageIs(options) {
			if err := extension.UnmarshalTo(options); err != nil {
				response.Outcome = &aop.RunTurnResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "invalid AIScan run options: "+err.Error())}
				return response
			}
			break
		}
	}
	var message *aop.Message
	if req.Input != nil {
		message = protobuf.CloneOf(req.Input)
	}
	_, err := rt.RunSession(ctx, req.SessionId, RunInput{
		TurnID: req.TurnId, Message: message, Continue: req.ContinueSession,
		MaxTurns: int(req.MaxTurns), EvalCriteria: options.EvalCriteria, EvalMaxRounds: int(options.EvalMaxRounds),
	})
	if err != nil {
		response.Outcome = &aop.RunTurnResponse_Rejected{Rejected: rejection("FAILED_PRECONDITION", err.Error())}
		return response
	}
	response.Outcome = &aop.RunTurnResponse_Accepted{Accepted: &aop.TurnReceipt{SessionId: req.SessionId, TurnId: req.TurnId, State: "running"}}
	return response
}

func (rt *AgentRuntime) CancelAOPTurn(req *aop.CancelTurnRequest) *aop.CancelTurnResponse {
	response := &aop.CancelTurnResponse{}
	if rt == nil || req == nil || strings.TrimSpace(req.SessionId) == "" || strings.TrimSpace(req.TurnId) == "" {
		response.Outcome = &aop.CancelTurnResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id and turn_id are required")}
		return response
	}
	if err := rt.CancelSessionRun(req.SessionId, req.TurnId); err != nil {
		response.Outcome = &aop.CancelTurnResponse_Rejected{Rejected: rejection("NOT_FOUND", err.Error())}
		return response
	}
	response.Outcome = &aop.CancelTurnResponse_Accepted{Accepted: &aop.TurnReceipt{SessionId: req.SessionId, TurnId: req.TurnId, State: "canceled"}}
	return response
}

func (rt *AgentRuntime) CloseAOPSession(ctx context.Context, req *aop.CloseSessionRequest) *aop.CloseSessionResponse {
	response := &aop.CloseSessionResponse{}
	if rt == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		response.Outcome = &aop.CloseSessionResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id is required")}
		return response
	}
	if err := rt.CloseSession(ctx, req.SessionId, SessionCloseReason(req.Reason)); err != nil {
		response.Outcome = &aop.CloseSessionResponse_Rejected{Rejected: rejection("FAILED_PRECONDITION", err.Error())}
		return response
	}
	response.Outcome = &aop.CloseSessionResponse_Accepted{Accepted: &aop.Session{Id: req.SessionId, State: "closed"}}
	return response
}

// RegisterNamespaces binds the existing session and command handlers to a
// caller-owned mux. Call once during assembly, before using the communication Host.
func (rt *AgentRuntime) RegisterNamespaces(mux *aop.NamespaceMux) error {
	if rt == nil || mux == nil {
		return fmt.Errorf("runtime and namespace mux are required")
	}
	if err := mux.Register(&aop.ProtocolMessage{}, rt.HandleCoreNamespace); err != nil {
		return err
	}
	if err := mux.Register(&types.CommandProtocolMessage{}, rt.HandleCommandNamespace); err != nil {
		return err
	}
	return nil
}

// HandleCoreNamespace implements session control for all existing transports.
func (rt *AgentRuntime) HandleCoreNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error {
	value, ok := message.(*aop.ProtocolMessage)
	if !ok {
		return fmt.Errorf("unexpected core namespace message %T", message)
	}
	reply := func(message protobuf.Message) error { return send(aop.Reply(envelope.Id, message)) }
	switch payload := value.Message.(type) {
	case *aop.ProtocolMessage_OpenSessionRequest:
		return reply(&aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: rt.OpenAOPSession(payload.OpenSessionRequest)}})
	case *aop.ProtocolMessage_RunTurnRequest:
		return reply(&aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{RunTurnResponse: rt.RunAOPTurn(ctx, payload.RunTurnRequest)}})
	case *aop.ProtocolMessage_CancelTurnRequest:
		return reply(&aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnResponse{CancelTurnResponse: rt.CancelAOPTurn(payload.CancelTurnRequest)}})
	case *aop.ProtocolMessage_CloseSessionRequest:
		return reply(&aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: rt.CloseAOPSession(ctx, payload.CloseSessionRequest)}})
	default:
		return fmt.Errorf("unsupported AOP core message")
	}
}

// HandleCommandNamespace implements the existing product command namespace.
func (rt *AgentRuntime) HandleCommandNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error {
	value, ok := message.(*types.CommandProtocolMessage)
	if !ok {
		return fmt.Errorf("unexpected command namespace message %T", message)
	}
	reply := func(message protobuf.Message) error { return send(aop.Reply(envelope.Id, message)) }
	request := value.GetRequest()
	if request == nil || strings.TrimSpace(request.Line) == "" {
		return reply(aop.NewProtocolError("INVALID_ARGUMENT", "command line is required"))
	}
	// Close cancels the context before taking mu and waiting for operations.
	// Admission must use the same gate so Add cannot race with an empty Wait.
	rt.mu.Lock()
	if err := rt.ctx.Err(); err != nil {
		rt.mu.Unlock()
		return reply(aop.NewProtocolError("COMMAND_FAILED", err.Error()))
	}
	rt.operations.Add(1)
	rt.mu.Unlock()
	go func() {
		defer rt.operations.Done()
		result, err := rt.CommandSession(ctx, request.SessionId, request.Line)
		if err != nil {
			_ = reply(aop.NewProtocolError("COMMAND_FAILED", err.Error()))
			return
		}
		_ = reply(&types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Result{Result: result}})
	}()
	return nil
}

func rejection(code, message string) *aop.Rejection {
	return &aop.Rejection{Code: code, Message: message}
}
