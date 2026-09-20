package client

import (
	"context"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/ioa/protocols"
)

type sessionRoute struct {
	deliver   func(context.Context, inbox.Message) error
	primary   bool
	delegated bool
	space     string
	dispatch  string
	start     hooks.SessionEvent
}

func (e *CollaborationExtension) sessionStart(ctx context.Context, ev hooks.SessionEvent) (struct{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	space, err := e.service.ReadySpace(ctx)
	if err != nil {
		return struct{}{}, err
	}
	route := &sessionRoute{deliver: ev.Deliver, primary: ev.Primary, delegated: ev.Delegation != nil, space: space, start: ev}
	e.mu.Lock()
	if _, exists := e.routes[ev.SessionID]; exists {
		e.mu.Unlock()
		return struct{}{}, fmt.Errorf("IOA session %q already registered", ev.SessionID)
	}
	// Reserve the identity; incoming messages cannot enter until recording succeeds.
	e.routes[ev.SessionID] = route
	route.deliver = nil
	e.mu.Unlock()
	if ev.Delegation != nil {
		msg, err := e.service.Client().Send(ctx, space, handoffBody(ev, "delegate", "delegated", ""))
		if err != nil {
			return struct{}{}, e.recordError(err)
		}
		e.mu.Lock()
		route.dispatch = msg.ID
		e.mu.Unlock()
	}
	e.mu.Lock()
	route.deliver = ev.Deliver
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

func handoffBody(ev hooks.SessionEvent, phase, status, ref string) protocols.SendMessage {
	detail := ev.Delegation
	body := protocols.SendMessage{ContentType: "handoff", Content: map[string]any{}, Meta: map[string]any{
		"source_session_id": ev.ParentID,
		"subagent": map[string]any{
			"phase": phase, "status": status, "name": ev.AgentName, "type": detail.AgentType, "mode": handoffMode(detail), "model": ev.Model,
			"parent_session_id": ev.ParentID, "parent_tool_call_id": ev.ParentToolCallID, "session_id": ev.SessionID,
		},
	}}
	if phase == "delegate" {
		body.Content["title"] = fmt.Sprintf("Delegate to subagent %q", ev.AgentName)
		body.Content["message"] = detail.Task
		body.Content["input"] = ev.Input
	} else {
		body.Meta["source_session_id"] = ev.SessionID
		body.Content["title"] = fmt.Sprintf("Return from subagent %q (%s)", ev.AgentName, status)
		body.Content["message"] = ev.Output
		if ev.Err != nil {
			body.Content["error"] = ev.Err.Error()
		}
		body.Refs = &protocols.Ref{Messages: []string{ref}}
	}
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
