package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

type AgentType struct {
	FormattedPrompt string
	Model           string
	Background      bool
}

type AgentTypeResolver func(name string) (AgentType, error)

type SubAgentTool struct {
	runtime *Runtime
	resolve AgentTypeResolver
}

func NewSubAgentTool(runtime *Runtime, resolve AgentTypeResolver) *SubAgentTool {
	return &SubAgentTool{runtime: runtime, resolve: resolve}
}

func (t *SubAgentTool) Name() string { return "subagent" }

func (t *SubAgentTool) Description() string {
	return "Create a subagent to handle an independent task. Modes: sync (block), async (background), fork (background with parent context for cache efficiency)."
}

type SubAgentArgs struct {
	Action  string `json:"action,omitempty"  jsonschema:"description=create: spawn subagent. list: show running. kill: cancel by session ID or name. Use ioa send for communication.,enum=create,enum=list,enum=kill"`
	Prompt  string `json:"prompt"            jsonschema:"description=Task description for the subagent (required for create)"`
	Mode    string `json:"mode,omitempty"    jsonschema:"description=sync: block until done. async: background with fresh context. fork: background inheriting parent conversation (cache-friendly). Default: async.,enum=sync,enum=async,enum=fork"`
	Type    string `json:"type,omitempty"    jsonschema:"description=Agent type name (a skill with agent:true)"`
	Name    string `json:"name,omitempty"    jsonschema:"description=Human-readable label for tracking"`
	Timeout string `json:"timeout,omitempty" jsonschema:"description=Optional timeout for sync mode (e.g. 30s or 2m). Returns error on timeout."`
}

func (t *SubAgentTool) Definition() *aop.ToolDefinition {
	return coretool.Def(t.Name(), t.Description(), SubAgentArgs{})
}

func (t *SubAgentTool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	args, err := coretool.ParseArgs[SubAgentArgs](arguments)
	if err != nil {
		return nil, err
	}

	switch args.Action {
	case "list":
		return coretool.TextResult(t.list()), nil
	case "kill":
		output, err := t.kill(args.Name)
		if err != nil {
			return nil, err
		}
		return coretool.TextResult(output), nil
	case "", "create":
		output, err := t.create(ctx, args.Prompt, args.Type, args.Name, args.Mode, args.Timeout)
		if err != nil {
			return coretool.TextResult(output), err
		}
		return coretool.TextResult(output), nil
	default:
		return nil, fmt.Errorf("unknown action: %s", args.Action)
	}
}

func (t *SubAgentTool) create(ctx context.Context, prompt, typeName, name, mode, timeout string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	cfg, ok := agent.ToolAgentConfig(ctx)
	if !ok {
		return "", fmt.Errorf("subagent create requires the executing agent context")
	}
	callID := operation.InvocationFromContext(ctx).CallID
	if callID == "" {
		return "", fmt.Errorf("subagent create requires the spawning tool call id")
	}
	if err := t.runtime.ready(); err != nil {
		return "", err
	}
	var resolved AgentType
	if typeName != "" && t.resolve != nil {
		var err error
		resolved, err = t.resolve(typeName)
		if err != nil {
			return "", err
		}
	}
	if name == "" {
		name = typeName
		if name == "" {
			name = labelFromPrompt(prompt)
		}
	}
	if mode == "" {
		mode = "async"
		if typeName != "" && !resolved.Background {
			mode = "sync"
		}
	}
	if mode != "sync" && mode != "async" && mode != "fork" {
		return "", fmt.Errorf("unknown subagent mode %q", mode)
	}
	var duration time.Duration
	if timeout != "" {
		var err error
		duration, err = time.ParseDuration(timeout)
		if err != nil || duration <= 0 || mode != "sync" {
			return "", fmt.Errorf("timeout requires sync mode and a positive duration")
		}
	}
	rt := t.runtime
	rt.mu.Lock()
	_, parent := rt.findSessionLocked(cfg.SessionID)
	if parent == nil || parent.ctx.Err() != nil || rt.ctx.Err() != nil {
		rt.mu.Unlock()
		return "", fmt.Errorf("parent session is not active")
	}
	for _, child := range rt.sessions {
		if child.agentName == name {
			name += "-" + aop.EnvelopeID()
			break
		}
	}
	// This dispatch, including asynchronous completion, is ordinary Runtime work.
	rt.operations.Add(1)
	rt.mu.Unlock()
	base := parent.ctx
	if mode == "sync" {
		base = ctx
	}
	childCtx, cancel := context.WithCancel(base)
	stopParent := context.AfterFunc(parent.ctx, cancel)
	stopTimeout := func() {}
	if duration > 0 {
		childCtx, stopTimeout = context.WithTimeout(childCtx, duration)
	}
	cleanup := func() { stopTimeout(); stopParent(); cancel(); rt.operations.Done() }
	detail := &types.DelegationDetail{Task: prompt, AgentName: name, AgentType: typeName, RunMode: types.DelegationRunBackground, ContextMode: types.DelegationContextFresh}
	if mode == "sync" {
		detail.RunMode = types.DelegationRunForeground
	}
	var messages []*aop.Message
	if mode == "fork" {
		detail.ContextMode = types.DelegationContextFork
		messages = truncateToLastCompleteBoundary(cfg.Messages)
	}
	cfg.Messages = nil
	cfg.Delegation = detail
	if resolved.Model != "" {
		cfg.Model = resolved.Model
	}
	if resolved.FormattedPrompt != "" {
		prompt = resolved.FormattedPrompt + "\n\n" + prompt
	}
	var completion inbox.Inbox
	if mode != "sync" {
		completion = cfg.Inbox
	}
	child, err := rt.OpenSession(childCtx, SessionOptions{ParentSessionID: cfg.SessionID, ParentToolCallID: callID, AgentName: name, Messages: messages, agentConfig: &cfg, input: prompt, completion: completion})
	if err != nil {
		cleanup()
		return "", err
	}
	id := child.ID()
	run, err := rt.RunSession(childCtx, id, RunInput{Continue: true})
	if err != nil {
		_ = rt.CloseSession(context.Background(), id, SessionCloseError)
		cleanup()
		return "", err
	}
	finish := func() (*agent.Result, error) {
		result, runErr := run.Wait()
		reason := SessionCloseCompleted
		if runErr != nil {
			reason = SessionCloseError
			if errors.Is(runErr, context.Canceled) {
				reason = SessionCloseCanceled
			}
		}
		return result, errors.Join(runErr, rt.CloseSession(context.Background(), id, reason))
	}
	if mode == "sync" {
		defer cleanup()
		result, err := finish()
		if err != nil {
			return fmt.Sprintf("subagent %q (session=%s) failed: %s\n%s", name, id, err, resultOutput(result)), err
		}
		return fmt.Sprintf("<subagent_result name=%q session_id=%q type=%q status=\"completed\">\n%s\n</subagent_result>", name, id, typeName, resultOutput(result)), nil
	}
	go func() {
		defer cleanup()
		_, _ = finish()
	}()
	return fmt.Sprintf("Started subagent %q (session=%s, mode=%s, type=%s). Will notify on completion.", name, id, mode, typeName), nil
}

func (t *SubAgentTool) list() string {
	rt := t.runtime
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out strings.Builder
	for id, state := range rt.sessions {
		if state.delegation == nil || state.ctx.Err() != nil {
			continue
		}
		fmt.Fprintf(&out, "  - %s (session=%s, type=%s)\n", state.agentName, id, state.delegation.AgentType)
	}
	if out.Len() == 0 {
		return "No subagents running."
	}
	return "Running subagents:\n" + out.String()
}

func (t *SubAgentTool) kill(name string) (string, error) {
	rt := t.runtime
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var found *sessionState
	for id, state := range rt.sessions {
		if state.delegation == nil || state.ctx.Err() != nil {
			continue
		}
		if id == name {
			found = state
			break
		}
		if state.agentName == name {
			if found != nil {
				return "", fmt.Errorf("ambiguous name %q; use session ID", name)
			}
			found = state
		}
	}
	if found == nil {
		return "", fmt.Errorf("no running subagent %q", name)
	}
	found.cancel()
	return fmt.Sprintf("Subagent %q canceled.", name), nil
}

func resultOutput(r *agent.Result) string {
	if r == nil {
		return ""
	}
	return r.Output
}

func subagentCompletion(r *agent.Result, err error) (string, string) {
	result := resultOutput(r)
	if err == nil {
		return "completed", result
	}
	status := "failed"
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timed_out"
	} else if errors.Is(err, context.Canceled) {
		status = "canceled"
	}
	if result != "" {
		return status, fmt.Sprintf("Error: %s\n\nPartial output:\n%s", err, result)
	}
	return status, fmt.Sprintf("Error: %s", err)
}

// A fork cannot inherit only some results of a parallel tool-call batch.
func truncateToLastCompleteBoundary(messages []*aop.Message) []*aop.Message {
	pending := make(map[string]bool)
	boundary := 0
	for index, message := range messages {
		if message == nil {
			continue
		}
		for _, call := range provider.MessageToolCalls(message) {
			pending[call.Id] = true
		}
		for _, content := range message.Content {
			if result := content.GetToolResult(); result != nil {
				delete(pending, result.CallId)
			}
		}
		if len(pending) == 0 {
			boundary = index + 1
		}
	}
	return cloneSessionMessages(messages[:boundary])
}

func labelFromPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) > 30 {
		prompt = prompt[:30]
	}
	words := strings.Fields(prompt)
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, "-")
}

// Completion is part of session closure, after the final delegation record and
// before the parent can release its inbox.
func (s *sessionState) notifyCompletion() {
	if s.producer == nil {
		return
	}
	defer s.producer.Done()
	if s.starting || s.result == nil {
		return
	}
	status, content := subagentCompletion(s.result, errors.Join(s.runErr, s.closeErr))
	detail := s.delegation
	msg := inbox.NewMessage(inbox.OriginSystem, "user", fmt.Sprintf("<subagent_completion name=%q session_id=%q type=%q status=%q>\n%s\n</subagent_completion>", s.agentName, s.id, detail.AgentType, status, content))
	msg.Meta = map[string]any{"subagent": s.agentName, "session_id": s.id, "type": detail.AgentType, "status": status}
	if err := s.completion.Push(msg); err != nil && s.runtime.Logger != nil {
		s.runtime.Logger.Warnf("inbox push subagent completion %s: %s", s.agentName, err)
	}
}
