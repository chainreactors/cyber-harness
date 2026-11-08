package client

import (
	"context"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/collaboration"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/ioa/protocols"
)

type sessionRoute struct {
	space    string
	dispatch string
	start    hooks.SessionEvent
}

func (e *CollaborationExtension) sessionStart(ctx context.Context, ev hooks.SessionEvent) (struct{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	space, err := e.service.ReadySpace(ctx)
	if err != nil {
		return struct{}{}, err
	}
	route := &sessionRoute{space: space, start: ev}
	e.mu.Lock()
	if _, exists := e.routes[ev.SessionID]; exists {
		e.mu.Unlock()
		return struct{}{}, fmt.Errorf("IOA session %q already registered", ev.SessionID)
	}
	// Reserve the identity; incoming messages cannot enter until recording succeeds.
	e.routes[ev.SessionID] = route
	route.start.Deliver = nil
	e.mu.Unlock()
	if ev.Delegation != nil {
		msg, err := e.service.Client().Send(ctx, space, handoffBody(ev, "delegate", "delegated", ""))
		if err != nil {
			e.mu.Lock()
			if e.routes[ev.SessionID] == route {
				delete(e.routes, ev.SessionID)
			}
			e.mu.Unlock()
			return struct{}{}, e.recordError(err)
		}
		e.mu.Lock()
		route.dispatch = msg.ID
		e.mu.Unlock()
	}
	e.mu.Lock()
	route.start.Deliver = ev.Deliver
	e.mu.Unlock()
	return struct{}{}, nil
}

func (e *CollaborationExtension) sessionEnd(ctx context.Context, ev hooks.SessionEvent) (struct{}, error) {
	e.mu.Lock()
	route := e.routes[ev.SessionID]
	delete(e.routes, ev.SessionID)
	e.mu.Unlock()
	if route == nil || route.dispatch == "" {
		return struct{}{}, nil
	}
	start := route.start
	start.Output, start.Stop, start.Err, start.Reason = ev.Output, ev.Stop, ev.Err, ev.Reason
	status := string(ev.Stop)
	if status == "" {
		status = ev.Reason
	}
	if status == "" {
		status = "completed"
	}
	_, err := e.service.Client().Send(ctx, route.space, handoffBody(start, "return", status, route.dispatch))
	return struct{}{}, e.recordError(err)
}

// RecordInboxHandoff writes a router-to-agent transfer to the same IOA handoff
// history used by SessionStart/SessionEnd. The stable application ID correlates
// retries; source IOA messages are referenced rather than copied as new input.
func (e *CollaborationExtension) RecordInboxHandoff(ctx context.Context, message mailbox.Message) error {
	if e.service == nil || message.Message == nil {
		return fmt.Errorf("IOA inbox handoff recorder is unavailable")
	}
	space := message.Meta.Space
	if space == "" {
		space = e.service.ReceiveSpace()
	}
	inputID := message.Meta.MessageID
	record := message.Meta.Record
	if record == nil || record.Event != collaboration.InboxRoute {
		return fmt.Errorf("IOA inbox route record is missing")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	targetName := record.Target.SessionID
	if targetName == "" {
		targetName = record.Target.TaskID
	}
	if record.Agent != nil && record.Agent.Name != "" {
		targetName = record.Agent.Name
	}
	content := map[string]any{
		"title":   fmt.Sprintf("Inbox handoff to %q", targetName),
		"message": provider.MessageText(message.Message),
	}
	body := protocols.SendMessage{ContentType: "handoff", Content: content, Meta: record.Meta()}
	if inputID != "" && message.Meta.Space == space {
		body.Refs = &protocols.Ref{Messages: []string{inputID}}
	}
	_, err := e.service.Client().Send(ctx, space, body)
	return err
}

func handoffBody(ev hooks.SessionEvent, phase, status, ref string) protocols.SendMessage {
	detail := ev.Delegation
	record := collaboration.Envelope{
		Version: collaboration.Version, Event: collaboration.SubagentDelegate,
		ID: "delegate-" + ev.SessionID, Status: status, CallID: ev.ParentToolCallID,
		Source: collaboration.Address{SessionID: ev.ParentID},
		Target: collaboration.Address{TaskID: ev.TaskID, SessionID: ev.SessionID},
		Agent:  &collaboration.Agent{Name: ev.AgentName, Type: detail.AgentType, Mode: handoffMode(detail), Model: ev.Model},
	}
	body := protocols.SendMessage{ContentType: "handoff", Content: map[string]any{}}
	if phase == "delegate" {
		body.Content["title"] = fmt.Sprintf("Delegate to subagent %q", ev.AgentName)
		body.Content["message"] = detail.Task
		body.Content["input"] = ev.Input
	} else {
		record.Event, record.ID = collaboration.SubagentReturn, "return-"+ev.SessionID
		record.Source, record.Target = record.Target, record.Source
		body.Content["title"] = fmt.Sprintf("Return from subagent %q (%s)", ev.AgentName, status)
		body.Content["message"] = ev.Output
		if ev.Err != nil {
			body.Content["error"] = ev.Err.Error()
		}
		body.Refs = &protocols.Ref{Messages: []string{ref}}
	}
	body.Meta = record.Meta()
	return body
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
