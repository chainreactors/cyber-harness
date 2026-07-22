package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/aop/x/delegation"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/ioa/protocols"
)

// subscribeIOAHandoff records the two subagent lifecycle boundaries as native
// IOA handoff messages by observing the agent AOP bus: a child session.start
// carrying a delegation extension is the delegation, and the matching child
// session.end is the return. The return references the delegation message so
// other IOA implementations can reconstruct the thread without aiscan-specific
// APIs.
func subscribeIOAHandoff(bus *eventbus.Bus[aop.Event], client protocols.ClientAPI, spaceName string, logger telemetry.Logger) {
	if bus == nil || client == nil || spaceName == "" {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	r := &ioaHandoffRecorder{
		client:    client,
		spaceName: spaceName,
		logger:    logger,
		events:    make(chan aop.Event, 256),
		pending:   make(map[string]*handoffState),
		bySession: make(map[string]string),
	}
	bus.Subscribe(func(event aop.Event) {
		select {
		case r.events <- event:
		default:
			r.logger.Warnf("ioa handoff queue full, dropping %s", event.Type)
		}
	})
	go r.run()
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

type ioaHandoffRecorder struct {
	client    protocols.ClientAPI
	spaceName string
	logger    telemetry.Logger
	events    chan aop.Event

	mu        sync.Mutex
	spaceID   string
	pending   map[string]*handoffState // parent tool call id -> state
	bySession map[string]string        // child session id -> parent tool call id
}

func (r *ioaHandoffRecorder) run() {
	for event := range r.events {
		switch event.Type {
		case aop.TypeSessionStart:
			r.onSessionStart(event)
		case aop.TypeMessage:
			r.onMessage(event)
		case aop.TypeSessionEnd:
			r.onSessionEnd(event)
		}
	}
}

func (r *ioaHandoffRecorder) onSessionStart(event aop.Event) {
	data, err := aop.DecodeData[aop.SessionStartData](event)
	if err != nil || data.ParentToolCallID == "" {
		return
	}
	detail, ok, err := delegation.Get(event)
	if err != nil || !ok {
		return
	}
	state := &handoffState{
		name:            detail.AgentName,
		typeName:        detail.AgentType,
		mode:            handoffMode(detail),
		model:           data.Model,
		parentSessionID: data.ParentSessionID,
		toolCallID:      data.ParentToolCallID,
		sessionID:       event.SessionID,
	}
	title, message := formatSubAgentHandoff(true, state.name, "delegated", detail.Task, nil)
	msgID, err := r.send("delegate", "delegated", state, title, message, "")
	if err != nil {
		r.logger.Warnf("record subagent handoff %s: %s", state.name, err)
		return
	}
	state.msgID = msgID
	r.mu.Lock()
	r.pending[state.toolCallID] = state
	r.bySession[state.sessionID] = state.toolCallID
	r.mu.Unlock()
}

func (r *ioaHandoffRecorder) onMessage(event aop.Event) {
	r.mu.Lock()
	toolCallID, ok := r.bySession[event.SessionID]
	r.mu.Unlock()
	if !ok {
		return
	}
	data, err := aop.DecodeData[aop.MessageData](event)
	if err != nil || data.Role != "assistant" {
		return
	}
	var sb strings.Builder
	for _, part := range data.Parts {
		if part.Type == aop.PartText {
			sb.WriteString(part.Text)
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

func (r *ioaHandoffRecorder) onSessionEnd(event aop.Event) {
	r.mu.Lock()
	toolCallID, ok := r.bySession[event.SessionID]
	var state *handoffState
	if ok {
		state = r.pending[toolCallID]
		delete(r.pending, toolCallID)
		delete(r.bySession, event.SessionID)
	}
	r.mu.Unlock()
	if state == nil {
		return
	}
	data, err := aop.DecodeData[aop.SessionEndData](event)
	if err != nil {
		return
	}
	status := data.Stop
	if status == string(agent.StopReasonError) {
		status = "failed"
	}
	if status == "" {
		status = "completed"
	}
	var runErr error
	if data.Error != "" {
		runErr = errors.New(data.Error)
	}
	title, message := formatSubAgentHandoff(false, state.name, status, state.output, runErr)
	if _, err := r.send("return", status, state, title, message, state.msgID); err != nil {
		r.logger.Warnf("record subagent return %s: %s", state.name, err)
	}
}

func (r *ioaHandoffRecorder) send(phase, status string, state *handoffState, title, message, refID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func (r *ioaHandoffRecorder) resolveSpace(ctx context.Context) (string, error) {
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

func handoffMode(detail delegation.DelegationDetail) string {
	if detail.ContextMode == delegation.DelegationDetailContextModeFork {
		return "fork"
	}
	if detail.RunMode == delegation.DelegationDetailRunModeForeground {
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
