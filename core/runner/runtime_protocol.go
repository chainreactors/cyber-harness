package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

// HandleProtocol handles the transport-neutral Agent Runtime control frames.
// The caller owns framing and I/O; AgentRuntime owns all Session and Run state.
func (rt *AgentRuntime) HandleProtocol(ctx context.Context, msg webproto.Message, send func(webproto.Message)) bool {
	if rt == nil || send == nil {
		return false
	}
	sendError := func(turnID, taskID string, err error) {
		payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
		send(webproto.Message{Type: webproto.TypeError, TurnID: turnID, TaskID: taskID, Payload: payload})
	}

	switch msg.Type {
	case webproto.TypeSessionOpen:
		var payload webproto.SessionOpenPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			sendError("", "", err)
			return true
		}
		session, err := rt.EnsureSession(SessionOptions{
			ID: payload.SessionID, ParentSessionID: payload.ParentSessionID, ParentToolCallID: payload.ParentToolCallID,
		})
		if err != nil {
			sendError("", "", err)
			return true
		}
		encoded, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: session.ID()})
		send(webproto.Message{Type: webproto.TypeSessionOpened, Payload: encoded})
		return true

	case webproto.TypeSessionClose:
		var payload webproto.SessionLifecyclePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			sendError("", "", err)
			return true
		}
		reason := SessionCloseReason(payload.Reason)
		if err := rt.CloseSession(ctx, payload.SessionID, reason); err != nil {
			sendError("", "", err)
			return true
		}
		encoded, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: payload.SessionID, Reason: string(reason)})
		send(webproto.Message{Type: webproto.TypeSessionClosed, Payload: encoded})
		return true

	case webproto.TypeRun:
		if strings.TrimSpace(msg.TurnID) == "" {
			sendError("", "", fmt.Errorf("run turn_id is required"))
			return true
		}
		var payload webproto.RunPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			sendError(msg.TurnID, "", err)
			return true
		}
		_, err := rt.RunSession(ctx, payload.SessionID, RunInput{
			TurnID: msg.TurnID, Parts: payload.Parts, NoEcho: payload.NoEcho, MaxTurns: payload.MaxTurns,
			EvalCriteria: payload.EvalCriteria, EvalMaxRounds: payload.EvalMaxRounds, Continue: payload.Continue,
		})
		if err != nil {
			sendError(msg.TurnID, "", err)
		}
		return true

	case webproto.TypeRunCancel:
		if err := rt.CancelRun(msg.TurnID); err != nil {
			sendError(msg.TurnID, "", err)
		}
		return true

	case webproto.TypeCommand:
		var payload webproto.CommandPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			sendError("", msg.TaskID, err)
			return true
		}
		rt.operations.Add(1)
		go func() {
			defer rt.operations.Done()
			result, err := rt.CommandSession(ctx, payload.SessionID, payload.Line)
			if err != nil {
				sendError("", msg.TaskID, err)
				return
			}
			encoded, _ := json.Marshal(webproto.CommandResultPayload{
				SessionID: payload.SessionID, Parts: result.Parts, Metadata: result.Metadata,
			})
			send(webproto.Message{Type: webproto.TypeCommandResult, TaskID: msg.TaskID, Payload: encoded})
		}()
		return true
	}
	return false
}
