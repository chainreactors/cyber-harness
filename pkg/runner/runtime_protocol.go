package runner

import (
	"context"
	"encoding/json"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	protobuf "google.golang.org/protobuf/proto"
)

const AIScanRunOptionsNamespace = "io.chainreactors.aiscan.run"

func RuntimeCommandSpecs() []*transport.CommandSpec {
	return []*transport.CommandSpec{
		{Name: "/status", Description: "Show Runtime session and provider status"},
		{Name: "/clear", Description: "Clear the current Agent context"},
		{Name: "/compact", Usage: "/compact [focus]", Description: "Compact the current Agent context"},
	}
}

func (rt *AgentRuntime) OpenAOPSession(req *aop.OpenSessionRequest) *aop.OpenSessionResponse {
	response := &aop.OpenSessionResponse{}
	if req != nil {
		response.RequestId = req.RequestId
	}
	if rt == nil || req == nil || strings.TrimSpace(req.SessionId) == "" {
		response.Outcome = &aop.OpenSessionResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id is required")}
		return response
	}
	session, err := rt.EnsureSession(SessionOptions{ID: req.SessionId, ParentSessionID: req.ParentSessionId, ParentToolCallID: req.ParentToolCallId})
	if err != nil {
		response.Outcome = &aop.OpenSessionResponse_Rejected{Rejected: rejection("FAILED_PRECONDITION", err.Error())}
		return response
	}
	response.Outcome = &aop.OpenSessionResponse_Accepted{Accepted: &aop.Session{Id: session.ID(), State: "open", Participant: req.Participant, Title: req.Title}}
	return response
}

func (rt *AgentRuntime) RunAOPTurn(ctx context.Context, req *aop.RunTurnRequest) *aop.RunTurnResponse {
	response := &aop.RunTurnResponse{}
	if req != nil {
		response.RequestId = req.RequestId
	}
	if rt == nil || req == nil || (!req.ContinueSession && req.Input == nil) || strings.TrimSpace(req.SessionId) == "" || strings.TrimSpace(req.TurnId) == "" {
		response.Outcome = &aop.RunTurnResponse_Rejected{Rejected: rejection("INVALID_ARGUMENT", "session_id, turn_id, and input are required unless continue_session is true")}
		return response
	}
	options := new(transport.RunOptions)
	for _, extension := range req.Extensions {
		if extension.GetNamespace() == AIScanRunOptionsNamespace {
			if err := aop.DecodeProtoJSON(extension.GetValue(), options); err != nil {
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
	if req != nil {
		response.RequestId = req.RequestId
	}
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
	if req != nil {
		response.RequestId = req.RequestId
	}
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

// HandleServerFrame is the generated-message control loop shared by stdio and
// other transports that host an AgentRuntime directly.
func (rt *AgentRuntime) HandleServerFrame(ctx context.Context, frame *transport.ServerFrame, send func(*transport.AgentFrame)) bool {
	if rt == nil || frame == nil || send == nil {
		return false
	}
	correlation := frame.CorrelationId
	switch payload := frame.Payload.(type) {
	case *transport.ServerFrame_OpenSession:
		send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_OpenSession{OpenSession: rt.OpenAOPSession(payload.OpenSession)}})
	case *transport.ServerFrame_RunTurn:
		send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_RunTurn{RunTurn: rt.RunAOPTurn(ctx, payload.RunTurn)}})
	case *transport.ServerFrame_CancelTurn:
		send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_CancelTurn{CancelTurn: rt.CancelAOPTurn(payload.CancelTurn)}})
	case *transport.ServerFrame_CloseSession:
		send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_CloseSession{CloseSession: rt.CloseAOPSession(ctx, payload.CloseSession)}})
	case *transport.ServerFrame_Command:
		request := payload.Command
		if request == nil || strings.TrimSpace(request.Line) == "" {
			send(operationError(correlation, request.GetTaskId(), "command line is required"))
			break
		}
		rt.operations.Add(1)
		go func() {
			defer rt.operations.Done()
			result, err := rt.CommandSession(ctx, request.SessionId, request.Line)
			if err != nil {
				send(operationError(correlation, request.TaskId, err.Error()))
				return
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				send(operationError(correlation, request.TaskId, err.Error()))
				return
			}
			send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_CommandResult{CommandResult: &transport.CommandResult{TaskId: request.TaskId, Result: encoded, MediaType: "application/json"}}})
		}()
	default:
		return false
	}
	return true
}

func rejection(code, message string) *aop.Rejection {
	return &aop.Rejection{Code: code, Message: message}
}

func operationError(correlation, taskID, message string) *transport.AgentFrame {
	return &transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_OperationError{OperationError: &transport.OperationError{TaskId: taskID, Code: "INVALID_ARGUMENT", Message: message}}}
}
