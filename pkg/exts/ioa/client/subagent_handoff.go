package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/ioa/protocols"
	"google.golang.org/protobuf/proto"
)

func newHandoff(ctx context.Context, client protocols.ClientAPI, spaceName string, logger telemetry.Logger, report func(error)) *ioaHandoffPublisher {
	return &ioaHandoffPublisher{ctx: ctx, client: client, spaceName: spaceName, logger: logger, report: report,
		pending: make(map[string]*handoffState), bySession: make(map[string]string)}
}

func consumeHandoff(stream *coreevents.Stream, publisher *ioaHandoffPublisher) (*eventbus.Subscription[*aop.Event], error) {
	return stream.Consume(eventbus.SubscribeOptions[*aop.Event]{Buffer: 256, MaxBytes: 16 << 20,
		Filter: func(event *aop.Event) bool {
			if event == nil {
				return false
			}
			switch event.Payload.(type) {
			case *aop.Event_SessionStarted, *aop.Event_Message, *aop.Event_TurnEnded:
				return true
			}
			return false
		},
		Size:  func(event *aop.Event) int64 { return int64(proto.Size(event)) },
		Clone: func(event *aop.Event) *aop.Event { return proto.Clone(event).(*aop.Event) },
	}, publisher)
}

type handoffState struct {
	msgID           string
	name            string
	typeName        string
	mode            string
	model           string
	parentSessionID string
	toolCallID      string
	sessionID       string
	output          string
}

type ioaHandoffPublisher struct {
	ctx       context.Context
	client    protocols.ClientAPI
	spaceName string
	logger    telemetry.Logger
	report    func(error)

	mu        sync.Mutex
	spaceID   string
	pending   map[string]*handoffState // parent tool call id -> state
	bySession map[string]string        // child session id -> parent tool call id
}

func (r *ioaHandoffPublisher) ConsumeEvent(event *aop.Event) error {
	if event == nil {
		return nil
	}
	switch event.Payload.(type) {
	case *aop.Event_SessionStarted:
		r.onSessionStart(event)
	case *aop.Event_Message:
		r.onMessage(event)
	case *aop.Event_TurnEnded:
		r.onTurnEnd(event)
	}
	// A transient network error must not terminate the event subscription.
	return nil
}

func (r *ioaHandoffPublisher) failed(err error) {
	if r.report != nil {
		r.report(err)
	}
}

func (r *ioaHandoffPublisher) onSessionStart(event *aop.Event) {
	data := event.GetSessionStarted()
	if data.ParentToolCallId == "" {
		return
	}
	detail, ok, err := types.GetDelegation(event)
	if err != nil || !ok {
		return
	}
	state := &handoffState{
		name:            detail.AgentName,
		typeName:        detail.AgentType,
		mode:            handoffMode(detail),
		model:           data.Model,
		parentSessionID: data.ParentSessionId,
		toolCallID:      data.ParentToolCallId,
		sessionID:       event.SessionId,
	}
	title, message := formatSubAgentHandoff(true, state.name, "delegated", detail.Task, nil)
	msgID, err := r.send("delegate", "delegated", state, title, message, "")
	if err != nil {
		r.logger.Warnf("record subagent handoff %s: %s", state.name, err)
		r.failed(err)
		return
	}
	state.msgID = msgID
	r.mu.Lock()
	r.pending[state.toolCallID] = state
	r.bySession[state.sessionID] = state.toolCallID
	r.mu.Unlock()
}

func (r *ioaHandoffPublisher) onMessage(event *aop.Event) {
	r.mu.Lock()
	toolCallID, ok := r.bySession[event.SessionId]
	r.mu.Unlock()
	if !ok {
		return
	}
	data := event.GetMessage()
	if data.Role != "assistant" {
		return
	}
	var sb strings.Builder
	for _, part := range data.Content {
		if text := part.GetText().GetText(); text != "" {
			sb.WriteString(text)
		}
	}
	if sb.Len() == 0 {
		return
	}
	r.mu.Lock()
	if state := r.pending[toolCallID]; state != nil {
		state.output = sb.String()
	}
	r.mu.Unlock()
}

func (r *ioaHandoffPublisher) onTurnEnd(event *aop.Event) {
	r.mu.Lock()
	toolCallID, ok := r.bySession[event.SessionId]
	var state *handoffState
	if ok {
		state = r.pending[toolCallID]
		delete(r.pending, toolCallID)
		delete(r.bySession, event.SessionId)
	}
	r.mu.Unlock()
	if state == nil {
		return
	}
	data := event.GetTurnEnded()
	status := data.StopReason
	if status == "error" {
		status = "failed"
	}
	if status == "" {
		status = "completed"
	}
	var runErr error
	if data.Error != nil {
		runErr = errors.New(data.Error.Message)
	}
	title, message := formatSubAgentHandoff(false, state.name, status, state.output, runErr)
	if _, err := r.send("return", status, state, title, message, state.msgID); err != nil {
		r.logger.Warnf("record subagent return %s: %s", state.name, err)
		r.failed(err)
	}
}

func (r *ioaHandoffPublisher) send(phase, status string, state *handoffState, title, message, refID string) (string, error) {
	if r == nil || isNilIOADependency(r.client) {
		return "", fmt.Errorf("IOA client is not configured")
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	spaceID, err := r.resolveSpace(ctx)
	if err != nil {
		return "", err
	}
	body := protocols.SendMessage{
		ContentType: "handoff",
		Content: map[string]any{
			"title":   title,
			"message": message,
		},
		Meta: map[string]any{
			"subagent": map[string]any{
				"phase":               phase,
				"status":              status,
				"name":                state.name,
				"type":                state.typeName,
				"mode":                state.mode,
				"model":               state.model,
				"parent_session_id":   state.parentSessionID,
				"parent_tool_call_id": state.toolCallID,
				"session_id":          state.sessionID,
			},
		},
	}
	if refID != "" {
		body.Refs = &protocols.Ref{Messages: []string{refID}}
	}
	msg, err := r.client.Send(ctx, spaceID, body)
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (r *ioaHandoffPublisher) resolveSpace(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spaceID != "" {
		return r.spaceID, nil
	}
	space, err := r.client.Space(ctx, r.spaceName, "aiscan agent")
	if err != nil {
		return "", fmt.Errorf("resolve IOA space %q: %w", r.spaceName, err)
	}
	r.spaceID = space.ID
	return r.spaceID, nil
}

func handoffMode(detail *types.DelegationDetail) string {
	if detail.ContextMode == types.DelegationContextFork {
		return "fork"
	}
	if detail.RunMode == types.DelegationRunForeground {
		return "sync"
	}
	return "async"
}

func formatSubAgentHandoff(delegate bool, name, status, text string, runErr error) (string, string) {
	if delegate {
		return fmt.Sprintf("Delegate to subagent %q", name), text
	}
	message := text
	if runErr != nil {
		if message == "" {
			message = runErr.Error()
		} else {
			message = fmt.Sprintf("%s\n\nPartial output:\n%s", runErr, message)
		}
	}
	return fmt.Sprintf("Return from subagent %q (%s)", name, status), message
}
