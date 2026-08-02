package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

func RuntimeCommandSpecs() []*types.CommandSpec {
	return []*types.CommandSpec{
		{Name: "/status", Description: "Show Runtime session and provider status"},
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
		message = protobuf.Clone(req.Input).(*aop.Message)
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

var runtimeEnvelopeSequence atomic.Uint64

func runtimeEnvelopeID() string {
	return "runtime:" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":" + strconv.FormatUint(runtimeEnvelopeSequence.Add(1), 36)
}

// HandleEnvelope is the protobuf control loop shared by stdio and other direct
// AgentRuntime hosts. The wire envelope is common; semantics remain in their
// AOP or AIScan namespace ProtocolMessage.
func (rt *AgentRuntime) HandleEnvelope(ctx context.Context, envelope *aop.Envelope, send func(*aop.Envelope)) bool {
	if rt == nil || envelope == nil || send == nil {
		return false
	}
	if rt.namespaceMux == nil {
		send(runtimeReply(envelope.Id, runtimeProtocolError("NAMESPACE_INIT_FAILED", "runtime namespaces are not initialized")))
		return true
	}
	handled, err := rt.namespaceMux.Dispatch(ctx, envelope, func(value *aop.Envelope) error { send(value); return nil })
	if err != nil {
		send(runtimeReply(envelope.Id, runtimeProtocolError("INVALID_PAYLOAD", err.Error())))
		return true
	}
	return handled
}

func newRuntimeNamespaceMux(rt *AgentRuntime) (*aop.NamespaceMux, error) {
	mux := aop.NewNamespaceMux()
	if err := mux.Register(&aop.ProtocolMessage{}, rt.handleCoreNamespace); err != nil {
		return nil, err
	}
	if err := mux.Register(&types.CommandProtocolMessage{}, rt.handleCommandNamespace); err != nil {
		return nil, err
	}
	return mux, nil
}

func (rt *AgentRuntime) handleCoreNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error {
	value := message.(*aop.ProtocolMessage)
	reply := func(message protobuf.Message) error { return send(runtimeReply(envelope.Id, message)) }
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

func (rt *AgentRuntime) handleCommandNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error {
	value := message.(*types.CommandProtocolMessage)
	reply := func(message protobuf.Message) error { return send(runtimeReply(envelope.Id, message)) }
	request := value.GetRequest()
	if request == nil || strings.TrimSpace(request.Line) == "" {
		return reply(runtimeProtocolError("INVALID_ARGUMENT", "command line is required"))
	}
	rt.operations.Add(1)
	go func() {
		defer rt.operations.Done()
		result, err := rt.CommandSession(ctx, request.SessionId, request.Line)
		if err != nil {
			_ = reply(runtimeProtocolError("COMMAND_FAILED", err.Error()))
			return
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			_ = reply(runtimeProtocolError("COMMAND_FAILED", err.Error()))
			return
		}
		_ = reply(&types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Result{Result: &types.CommandResult{Data: encoded, MediaType: aop.JSONMediaType}}})
	}()
	return nil
}

// ServeEnvelopeStream is the framing-independent runtime loop. WebSocket and
// stdio decide only how an Envelope is read and written; protobuf dispatch and
// reply correlation stay here.
func (rt *AgentRuntime) ServeEnvelopeStream(ctx context.Context, stream aop.EnvelopeStream) error {
	if rt == nil || stream == nil {
		return fmt.Errorf("runtime envelope stream is required")
	}
	for {
		envelope, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		handled := rt.HandleEnvelope(ctx, envelope, func(response *aop.Envelope) {
			_ = stream.Send(response)
		})
		if !handled {
			if err := stream.Send(runtimeReply(envelope.GetId(), runtimeProtocolError("UNSUPPORTED_MESSAGE", "unsupported protocol message"))); err != nil {
				return err
			}
		}
	}
}

func rejection(code, message string) *aop.Rejection {
	return &aop.Rejection{Code: code, Message: message}
}

func runtimeReply(replyTo string, message protobuf.Message) *aop.Envelope {
	envelope, err := aop.Wrap(runtimeEnvelopeID(), replyTo, message)
	if err != nil {
		panic(fmt.Sprintf("wrap runtime protocol message: %v", err))
	}
	return envelope
}

func runtimeProtocolError(code, message string) *aop.ProtocolMessage {
	return &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{Code: code, Message: message}}}
}
