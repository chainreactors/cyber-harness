package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

type AgentType struct {
	FormattedPrompt string
	Model           string
	Background      bool
}

type AgentTypeResolver func(name string) (AgentType, error)

type subAgentInfo struct {
	Name      string
	Type      string
	Mode      string
	StartedAt time.Time
	Cancel    context.CancelFunc
	SessionID string
}

type SubAgentTool struct {
	resolve AgentTypeResolver
	mu      sync.Mutex
	running map[string]*subAgentInfo
	closed  bool
	workers sync.WaitGroup
}

func NewSubAgentTool(resolve AgentTypeResolver) *SubAgentTool {
	return &SubAgentTool{
		resolve: resolve,
		running: make(map[string]*subAgentInfo),
	}
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
	task := prompt

	var resolved *AgentType
	if typeName != "" && t.resolve != nil {
		at, err := t.resolve(typeName)
		if err != nil {
			return "", err
		}
		resolved = &at
	}

	if name == "" {
		if typeName != "" {
			name = typeName
		} else {
			name = labelFromPrompt(prompt)
		}
	}
	name = t.uniqueName(name)

	if mode == "" {
		mode = "async"
		if resolved != nil && !resolved.Background {
			mode = "sync"
		}
	}

	parent, parentInbox, err := t.executionParent(ctx)
	if err != nil {
		return "", err
	}
	parentToolCallID := operation.InvocationFromContext(ctx).CallID
	if parentToolCallID == "" {
		return "", fmt.Errorf("subagent create requires the spawning tool call id")
	}
	detail := delegationDetail(task, typeName, name, mode)
	parentCfg := parent.configSnapshot()
	sub := deriveNamedFromConfig(parentCfg, name, parentToolCallID, detail)
	if resolved != nil {
		if resolved.FormattedPrompt != "" {
			prompt = resolved.FormattedPrompt + "\n\n" + prompt
		}
		if resolved.Model != "" {
			sub.SetProvider(parentCfg.Provider, resolved.Model)
		}
	}
	if mode == "fork" {
		// Run rebuilds cfg.Messages from the agent state, so seeding the state is
		// what actually hands the parent conversation to the child.
		sub.LoadMessages(truncateToLastCompleteBoundary(parentCfg.Messages))
	}

	return t.runTask(ctx, sub, prompt, name, typeName, mode, timeout, parentInbox, parentCfg.Logger)
}

func delegationFromToolCall(toolName string, args any) (*types.DelegationDetail, bool) {
	if toolName != "subagent" {
		return nil, false
	}
	values, ok := args.(map[string]any)
	if !ok {
		return nil, false
	}
	if action, _ := values["action"].(string); action != "" && action != "create" {
		return nil, false
	}
	task, _ := values["prompt"].(string)
	if strings.TrimSpace(task) == "" {
		return nil, false
	}
	name, _ := values["name"].(string)
	typeName, _ := values["type"].(string)
	mode, _ := values["mode"].(string)
	return delegationDetail(task, typeName, name, mode), true
}

func delegationDetail(task, typeName, name, mode string) *types.DelegationDetail {
	detail := &types.DelegationDetail{
		Task:      task,
		AgentName: name,
		AgentType: typeName,
	}
	switch mode {
	case "sync":
		detail.RunMode = types.DelegationRunForeground
		detail.ContextMode = types.DelegationContextFresh
	case "async":
		detail.RunMode = types.DelegationRunBackground
		detail.ContextMode = types.DelegationContextFresh
	case "fork":
		detail.RunMode = types.DelegationRunBackground
		detail.ContextMode = types.DelegationContextFork
	}
	return detail
}

// runTask admits and records a task synchronously, including background tasks.
func (t *SubAgentTool) runTask(ctx context.Context, sub *Agent, input, name, typeName, mode, timeout string, parentInbox inbox.Inbox, logger telemetry.Logger) (string, error) {
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
	base := ctx
	if mode != "sync" {
		base = context.WithoutCancel(ctx)
	}
	taskCtx, cancel := context.WithCancel(base)
	stopLifetime := func() bool { return false }
	if sub.Cfg.Lifetime != nil {
		stopLifetime = context.AfterFunc(sub.Cfg.Lifetime, cancel)
	}
	stopTimeout := func() {}
	if duration > 0 {
		var c context.CancelFunc
		taskCtx, c = context.WithTimeout(taskCtx, duration)
		stopTimeout = c
	}
	sub.Cfg.Lifetime = taskCtx
	sub.Cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	id := sub.SessionID()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel()
		stopLifetime()
		stopTimeout()
		return "", fmt.Errorf("subagent tool is closed")
	}
	t.running[id] = &subAgentInfo{Name: name, Type: typeName, Mode: mode, StartedAt: time.Now(), Cancel: cancel, SessionID: id}
	t.workers.Add(1)
	t.mu.Unlock()
	cleanup := func() { cancel(); stopLifetime(); stopTimeout(); t.untrack(id); t.workers.Done() }
	if err := sub.Cfg.Inbox.Push(inbox.FromAOPMessage(TextInput(input), inbox.OriginUser)); err != nil {
		cleanup()
		return "", err
	}
	ev := sessionEvent(sub.configSnapshot(), "")
	ev.Input = input
	ev.Deliver = func(ctx context.Context, msg inbox.Message) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := taskCtx.Err(); err != nil {
			return err
		}
		return sub.Cfg.Inbox.Push(msg)
	}
	// The dispatch call's deadline still bounds registration for async execution.
	startCtx, cancelStart := context.WithCancel(ctx)
	stopStart := context.AfterFunc(taskCtx, cancelStart)
	_, err := hooks.SessionStart.Emit(startCtx, sub.Cfg.Hooks, ev)
	stopStart()
	cancelStart()
	if err == nil {
		err = taskCtx.Err()
	}
	if err != nil {
		sub.Cfg.Inbox.Close()
		ev.Reason, ev.Stop, ev.Err = "start_failed", StopReasonError, err
		endCtx, endCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, endErr := hooks.SessionEnd.Emit(endCtx, sub.Cfg.Hooks, ev)
		endCancel()
		cleanup()
		return "", errors.Join(err, endErr)
	}
	sub.Cfg.emitter.sessionStart(sub.Cfg.Model)
	if mode == "sync" {
		defer cleanup()
		r, err := runDerivedSession(taskCtx, sub)
		if err != nil {
			return fmt.Sprintf("subagent %q (session=%s) failed: %s\n%s", name, id, err, resultOutput(r)), err
		}
		return fmt.Sprintf("<subagent_result name=%q session_id=%q type=%q status=\"completed\">\n%s\n</subagent_result>", name, id, typeName, resultOutput(r)), nil
	}
	producer := parentInbox.RegisterProducer("subagent:" + id)
	go func() {
		defer cleanup()
		defer producer.Done()
		r, err := runDerivedSession(taskCtx, sub)
		t.pushCompletion(parentInbox, logger, id, name, typeName, r, err)
	}()
	return fmt.Sprintf("Started subagent %q (session=%s, mode=%s, type=%s). Will notify on completion.", name, id, mode, typeName), nil
}

func runDerivedSession(ctx context.Context, sub *Agent) (result *Result, err error) {
	turnID := randomID()
	emitter := sub.configSnapshot().emitter.turn(turnID)
	emitter.turnStart()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("subagent execution panicked: %v", recovered)
		}
		sub.Cfg.Inbox.Close()
		stop := StopReasonError
		var usage *aop.TokenUsage
		tokens := 0
		if result != nil {
			stop, usage, tokens = result.Stop, result.TotalUsage, result.ContextTokens
		}
		if err != nil {
			stop = StopReasonError
		}
		if errors.Is(err, context.Canceled) {
			stop = StopReasonCanceled
		}
		if stop == "" {
			stop = StopReasonCompleted
		}
		emitter.turnEnd(stop, usage, tokens, err)
		ev := sessionEvent(sub.configSnapshot(), string(stop))
		ev.Output, ev.Stop, ev.Err = resultOutput(result), stop, err
		endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, endErr := hooks.SessionEnd.Emit(endCtx, sub.Cfg.Hooks, ev)
		err = errors.Join(err, endErr)
		sub.Cfg.emitter.sessionEnd(string(stop))
	}()
	return sub.run(ctx, nil, WithTurnID(turnID))
}

// Close revokes admission before waiting, so new tasks cannot race the join.
func (t *SubAgentTool) Close(ctx context.Context) error {
	t.mu.Lock()
	t.closed = true
	for _, task := range t.running {
		task.Cancel()
	}
	t.mu.Unlock()
	done := make(chan struct{})
	go func() { t.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *SubAgentTool) pushCompletion(parentInbox inbox.Inbox, logger telemetry.Logger, sessionID, name, typeName string, r *Result, err error) {
	status, content := subagentCompletion(r, err)

	msg := inbox.NewMessage(inbox.OriginSystem, "user",
		fmt.Sprintf("<subagent_completion name=%q session_id=%q type=%q status=%q>\n%s\n</subagent_completion>", name, sessionID, typeName, status, content))
	msg.Meta = map[string]any{"subagent": name, "session_id": sessionID, "type": typeName, "status": status}
	if err := parentInbox.Push(msg); err != nil {
		logger.Warnf("inbox push subagent completion %s: %s", name, err)
	}
}

func (t *SubAgentTool) executionParent(ctx context.Context) (*Agent, inbox.Inbox, error) {
	cfg, ok := toolAgentConfig(ctx)
	if !ok {
		return nil, nil, fmt.Errorf("subagent create requires the executing agent context")
	}
	return NewAgent(cfg), cfg.Inbox, nil
}

func resultOutput(r *Result) string {
	if r == nil {
		return ""
	}
	return r.Output
}

func subagentCompletion(r *Result, err error) (string, string) {
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

func (t *SubAgentTool) list() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.running) == 0 {
		return "No subagents running."
	}
	var sb strings.Builder
	sb.WriteString("Running subagents:\n")
	for id, info := range t.running {
		elapsed := time.Since(info.StartedAt).Round(time.Second)
		sb.WriteString(fmt.Sprintf("  - %s (session=%s, type=%s, mode=%s, running %s)\n", info.Name, id, info.Type, info.Mode, elapsed))
	}
	return sb.String()
}

func (t *SubAgentTool) kill(name string) (string, error) {
	t.mu.Lock()
	info := t.running[name]
	if info == nil {
		for _, candidate := range t.running {
			if candidate.Name == name {
				if info != nil {
					t.mu.Unlock()
					return "", fmt.Errorf("ambiguous name %q; use session ID", name)
				}
				info = candidate
			}
		}
	}
	t.mu.Unlock()
	if info == nil {
		return "", fmt.Errorf("no running subagent %q", name)
	}
	info.Cancel()
	return fmt.Sprintf("Subagent %q canceled.", name), nil
}

func (t *SubAgentTool) untrack(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.running, name)
}

func (t *SubAgentTool) uniqueName(base string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	exists := false
	for _, info := range t.running {
		if info.Name == base {
			exists = true
			break
		}
	}
	if !exists {
		return base
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return base + "-" + hex.EncodeToString(b)
}

func truncateToLastCompleteBoundary(messages []*aop.Message) []*aop.Message {
	out := append([]*aop.Message(nil), messages...)
	for i := len(out) - 1; i >= 0; i-- {
		msg := out[i]
		if msg.Role == "tool" || msg.Role == "user" {
			return out[:i+1]
		}
		if msg.Role == "assistant" && len(provider.MessageToolCalls(msg)) == 0 {
			return out[:i+1]
		}
	}
	return nil
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
