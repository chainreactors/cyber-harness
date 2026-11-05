package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	coretool "github.com/chainreactors/cyber/core/tool"
)

// routing is the sole tool executor for one envelope. It owns per-recipient
// acknowledgments, so repeated or parallel model calls cannot deliver twice.
type routing struct {
	router  *Router
	receipt *Receipt
	mu      sync.Mutex
	targets []string
	failed  map[string]error
	lastErr error
}

type routeArgs struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Live destination Session ID or unique agent name."`
	TaskID    string `json:"task_id,omitempty" jsonschema:"description=Persistent destination task ID; omit action or use action=route. Never combine with reply/record."`
	NewTask   *struct {
		Name  string `json:"name,omitempty" jsonschema:"description=Registered subagent name; omit for anonymous."`
		Label string `json:"label,omitempty"`
	} `json:"new_task,omitempty" jsonschema:"description=Route a new request to a fresh subagent task; execution is owned by the backend."`
	Action string `json:"action,omitempty" jsonschema:"description=route (default) transfers to a destination. reply/record are destinationless terminal acknowledgments; owned feedback replies to the ORIGINAL requester. To hand off to a coordinator use route+task_id.,enum=route,enum=record,enum=reply"`
}

func (t *routing) ToolDefinitions() []*aop.ToolDefinition {
	if t.router.active == nil {
		return []*aop.ToolDefinition{coretool.Def("inbox_route", "Forward the original envelope to a live agent.", struct {
			SessionID string `json:"session_id"`
		}{})}
	}
	return []*aop.ToolDefinition{coretool.Def("inbox_route", "Route the ORIGINAL envelope. To transfer feedback to an existing coordinator use {task_id: coordinator_id} (action omitted or route). To finish and deliver owned feedback to its ORIGINAL requester use {action: reply} or {action: record}, with NO destination. Never execute work yourself. Admission is not task completion.", routeArgs{})}
}

func (t *routing) ExecuteTool(ctx context.Context, name, arguments string) (result *aop.ToolResult, err error) {
	if name != "inbox_route" {
		return nil, fmt.Errorf("inbox router cannot execute tool %q", name)
	}
	args, err := coretool.ParseArgs[routeArgs](arguments)
	if err != nil {
		return nil, err
	}
	if t.router.active == nil && strings.TrimSpace(args.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	defer func() {
		if err != nil {
			t.lastErr = err
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.router.active != nil {
		return t.executeActive(ctx, args)
	}
	destination, err := t.router.resolve(args.SessionID)
	if err != nil {
		return nil, err
	}
	if address := t.receipt.message.Meta.TargetSessionID; address != "" {
		required, err := t.router.resolve(address)
		if err != nil {
			return nil, err
		}
		if required.ID != destination.ID {
			return nil, fmt.Errorf("message is addressed to %q; cannot route to %q", required.ID, destination.ID)
		}
	}
	if !slices.Contains(t.targets, destination.ID) {
		if err := destination.deliver(ctx, cloneMessage(t.receipt.message)); err != nil {
			failure := fmt.Errorf("deliver to %s: %w", destination.ID, err)
			t.failed[destination.ID] = failure
			return nil, failure
		}
		delete(t.failed, destination.ID)
		t.targets = append(t.targets, destination.ID)
	}
	return coretool.TextResult("Message " + t.receipt.ID + " admitted by " + destination.ID + "; task completion is independent."), nil
}

func (e *Extension) route(ag *agent.Agent, providers *provider.State, stream *events.Stream, receipt *Receipt) {
	r := e.router
	call := &routing{router: r, receipt: receipt, failed: make(map[string]error)}
	turnID := aop.EnvelopeID()
	var result *agent.Result
	var routeErr error
	defer func() {
		if failure := recover(); failure != nil {
			routeErr = fmt.Errorf("inbox routing panicked: %v", failure)
		}
		ended := &aop.TurnEnded{StopReason: "completed"}
		if result != nil {
			ended.Usage = result.TotalUsage
			ended.StopReason = string(result.Stop)
		}
		if routeErr != nil {
			ended.StopReason = "error"
			ended.Error = &aop.ProtocolError{Message: routeErr.Error()}
		}
		stream.Publish(&aop.Event{SessionId: r.sessionID, TurnId: turnID, Emitter: "inbox", Payload: &aop.Event_TurnEnded{TurnEnded: ended}})
		r.finish(receipt, slices.Clone(call.targets), routeErr)
	}()
	if r.active != nil {
		planned, err := r.active.store.routeTargets(messageKey(receipt.key))
		if err != nil {
			routeErr = err
			return
		}
		if len(planned) > 0 {
			for _, target := range planned {
				if err = call.activeDestination(r.ctx, target); err != nil {
					routeErr = errors.Join(routeErr, err)
				}
			}
			return
		}
	}
	stream.Publish(&aop.Event{SessionId: r.sessionID, TurnId: turnID, Emitter: "inbox", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
	p, config := providers.Current()
	ag.SetProviderConfig(p, config)
	ag.Cfg.Tools = call

	r.mu.Lock()
	catalog := make([]target, 0, len(r.targets))
	for _, value := range r.targets {
		catalog = append(catalog, value)
	}
	r.mu.Unlock()
	slices.SortFunc(catalog, func(a, b target) int { return strings.Compare(a.ID, b.ID) })
	// Routing sees text and media descriptors only. Passing media to Agent.Run
	// would resolve image URIs and read files, which belongs to the recipient.
	metadataFields := map[string]any{
		"message_id": receipt.ID, "origin": receipt.message.Origin,
		"meta": receipt.message.Meta, "priority": receipt.message.Priority,
		"interrupt": receipt.message.Interrupt, "created_at": receipt.message.CreatedAt,
		"attachments": receipt.message.Attachments, "sessions": catalog,
	}
	if r.active != nil {
		tasks, taskErr := r.active.store.tasks()
		if taskErr != nil {
			routeErr = taskErr
			return
		}
		metadataFields["tasks"] = tasks
		names := make([]string, 0)
		for _, definition := range r.active.executor.Catalog() {
			names = append(names, definition.Name)
		}
		slices.Sort(names)
		metadataFields["registered_subagents"] = names
	}
	metadata, err := json.Marshal(metadataFields)
	if err != nil {
		routeErr = fmt.Errorf("encode routing context: %w", err)
		return
	}
	input := &aop.Message{Role: "user", Content: []*aop.Content{aop.Text("Current routing context:\n" + string(metadata) + "\nOriginal message:\n")}}
	for _, part := range receipt.message.Message.Content {
		if text := part.GetText(); text != nil {
			input.Content = append(input.Content, aop.Text(text.Text))
		} else if media := part.GetMedia(); media != nil {
			resource := media.GetResource()
			input.Content = append(input.Content, aop.Text(fmt.Sprintf("\n[media kind=%q type=%q filename=%q uri=%q transcript=%q]", media.Kind, resource.GetMediaType(), resource.GetFilename(), resource.GetUri(), media.Transcript)))
		}
	}
	result, routeErr = ag.Run(r.ctx, input, agent.WithTurnID(turnID))
	for _, err := range call.failed {
		routeErr = errors.Join(routeErr, err)
	}
	if routeErr == nil && len(call.targets) == 0 {
		routeErr = errors.Join(fmt.Errorf("inbox agent finished without routing message %s", receipt.ID), call.lastErr)
	}
}

var _ coretool.Executor = (*routing)(nil)

func (t *routing) executeActive(ctx context.Context, args routeArgs) (*aop.ToolResult, error) {
	a := t.router.active
	key := messageKey(t.receipt.key)
	action := args.Action
	kind := t.receipt.message.Meta.Kind
	feedback := kind == "subagent_feedback" || t.receipt.message.Origin == mailbox.OriginPeer && kind == "task_result"
	if action == "" {
		action = "route"
	}
	if action != "route" {
		if args.SessionID != "" || args.TaskID != "" || args.NewTask != nil {
			return nil, fmt.Errorf("action=%s replies to the ORIGINAL requester and cannot have a destination; to transfer this envelope to the intended coordinator, keep its destination and use action=route (or omit action)", action)
		}
		if action == "reply" && kind != "subagent_feedback" {
			return nil, fmt.Errorf("reply requires subagent feedback")
		}
		if action == "record" && t.receipt.message.Origin != mailbox.OriginSystem && !feedback {
			return nil, fmt.Errorf("only system events or peer task results may be recorded without delivery")
		}
		if action != "reply" && action != "record" {
			return nil, fmt.Errorf("unknown routing action %q", action)
		}
		if err := a.store.planRoute(key, action); err != nil {
			return nil, err
		}
		err := t.activeDestination(ctx, action)
		return coretool.TextResult("Acknowledged " + action), err
	}
	count := 0
	if args.SessionID != "" {
		count++
	}
	if args.TaskID != "" {
		count++
	}
	if args.NewTask != nil {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("choose exactly one of session_id, task_id, new_task")
	}
	if args.SessionID != "" {
		destination, err := a.destination(args.SessionID)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(destination, "task:") {
			args.TaskID, args.SessionID = strings.TrimPrefix(destination, "task:"), ""
		} else {
			args.SessionID = strings.TrimPrefix(destination, "session:")
		}
	}
	if feedback && args.NewTask != nil {
		return nil, fmt.Errorf("feedback cannot implicitly create another task; use reply, record, or an existing task")
	}
	if required := t.receipt.message.Meta.TaskID; required != "" && kind != "subagent_feedback" {
		if args.TaskID != required {
			return nil, fmt.Errorf("message is addressed to task %q", required)
		}
	}
	if required := t.receipt.message.Meta.TargetSessionID; required != "" {
		destination, err := a.destination(required)
		if err != nil {
			return nil, err
		}
		if destination != "task:"+args.TaskID && destination != "session:"+args.SessionID {
			return nil, fmt.Errorf("message is addressed to %q", destination)
		}
	}
	if feedback && args.TaskID != "" {
		chain := messageChain(t.receipt.message)
		if slices.Contains(chain, args.TaskID) || len(chain) >= 16 {
			return nil, fmt.Errorf("feedback cycle or hop limit; use reply or record")
		}
	}
	if args.NewTask != nil || args.TaskID != "" {
		name, label := "", ""
		if args.NewTask != nil {
			name, label = args.NewTask.Name, args.NewTask.Label
			if name != "" {
				registered := false
				for _, definition := range a.executor.Catalog() {
					if definition.Name == name {
						registered = true
						break
					}
				}
				if !registered {
					return nil, fmt.Errorf("unknown registered subagent %q; omit new_task.name for an anonymous task and keep its role in new_task.label, or choose a name from registered_subagents", name)
				}
			}
		}
		id, err := a.store.enqueue(t.receipt, args.TaskID, name, label)
		if err != nil {
			return nil, err
		}
		target := "task:" + id
		if err := t.activeDestination(ctx, target); err != nil {
			return nil, err
		}
		a.deliverLive(id)
		a.signal()
		return coretool.TextResult("Task " + id + " admitted; execution is independent."), nil
	}
	destination, err := t.router.resolve(args.SessionID)
	if err != nil {
		return nil, err
	}
	target := "session:" + destination.ID
	if err = a.store.planRoute(key, target); err != nil {
		return nil, err
	}
	err = t.activeDestination(ctx, target)
	return coretool.TextResult("Message admitted by " + destination.ID), err
}

// Execution IDs (including old attempts) and unique live names must converge
// before address validation, feedback guards, and durable admission.
func (a *activeRuntime) destination(address string) (string, error) {
	destination, resolveErr := a.extension.router.resolve(address)
	var taskID string
	// Exact historical IDs take precedence over a live name with the same text.
	err := a.store.db.QueryRow("SELECT task_id FROM attempts WHERE session_id IN (?,?) ORDER BY session_id=? DESC LIMIT 1", address, destination.ID, address).Scan(&taskID)
	if err == nil {
		return "task:" + taskID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if resolveErr != nil {
		return "", resolveErr
	}
	return "session:" + destination.ID, nil
}

func (t *routing) activeDestination(ctx context.Context, target string) (err error) {
	defer func() {
		if err != nil {
			t.failed[target] = err
		} else {
			delete(t.failed, target)
		}
	}()
	a := t.router.active
	key := messageKey(t.receipt.key)
	done, err := a.store.hasRoute(key, target)
	if err != nil {
		return err
	}
	if !done {
		switch {
		case target == "reply":
			err = a.reply(t.receipt)
		case target == "record":
			// Includes replayed plans: an owned completion must reach its requester
			// unless it was explicitly handed off to an existing coordinator.
			if t.receipt.message.Meta.Kind == "subagent_feedback" {
				err = a.reply(t.receipt)
			}
		case strings.HasPrefix(target, "session:"):
			destination, lookupErr := t.router.resolve(strings.TrimPrefix(target, "session:"))
			err = lookupErr
			if err == nil {
				err = destination.deliver(ctx, cloneMessage(t.receipt.message))
			}
		case strings.HasPrefix(target, "task:"):
			_, err = a.store.enqueue(t.receipt, strings.TrimPrefix(target, "task:"), "", "")
		default:
			err = fmt.Errorf("unknown stored route %q", target)
		}
		if err != nil {
			return err
		}
		if err = a.store.markRoute(key, target); err != nil {
			return err
		}
	}
	if strings.HasPrefix(target, "task:") || strings.HasPrefix(target, "session:") {
		if err = a.recordHandoff(t.receipt, target); err != nil {
			return err
		}
	}
	if !slices.Contains(t.targets, target) {
		t.targets = append(t.targets, target)
	}
	return nil
}
