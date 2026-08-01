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

	"github.com/chainreactors/aiscan/agent/inbox"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
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
	Inbox     inbox.Inbox
}

type SubAgentTool struct {
	resolve AgentTypeResolver
	mu      sync.Mutex
	running map[string]*subAgentInfo
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
	Action  string `json:"action,omitempty"  jsonschema:"description=create: spawn subagent. list: show running. kill: cancel by name. message: send message to running subagent.,enum=create,enum=list,enum=kill,enum=message"`
	Prompt  string `json:"prompt"            jsonschema:"description=Task description for the subagent (required for create)"`
	Mode    string `json:"mode,omitempty"    jsonschema:"description=sync: block until done. async: background with fresh context. fork: background inheriting parent conversation (cache-friendly). Default: async.,enum=sync,enum=async,enum=fork"`
	Type    string `json:"type,omitempty"    jsonschema:"description=Agent type name (a skill with agent:true)"`
	Name    string `json:"name,omitempty"    jsonschema:"description=Human-readable label for tracking"`
	Message string `json:"message,omitempty" jsonschema:"description=Message to send (action=message requires name)"`
	Timeout string `json:"timeout,omitempty" jsonschema:"description=Optional timeout for sync mode (e.g. 30s or 2m). Returns error on timeout."`
}

func (t *SubAgentTool) Definition() ToolDefinition {
	return tool.Def(t.Name(), t.Description(), SubAgentArgs{})
}

func (t *SubAgentTool) Execute(ctx context.Context, arguments string) (tool.Result, error) {
	args, err := tool.ParseArgs[SubAgentArgs](arguments)
	if err != nil {
		return tool.Result{}, err
	}

	switch args.Action {
	case "list":
		return tool.TextResult(t.list()), nil
	case "kill":
		output, err := t.kill(args.Name)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.TextResult(output), nil
	case "message":
		output, err := t.sendMessage(args.Name, args.Message)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.TextResult(output), nil
	case "", "create":
		output, err := t.create(ctx, args.Prompt, args.Type, args.Name, args.Mode, args.Timeout)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.TextResult(output), nil
	default:
		return tool.Result{}, fmt.Errorf("unknown action: %s", args.Action)
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
	parentToolCallID := output.CallIDFromContext(ctx)
	if parentToolCallID == "" {
		return "", fmt.Errorf("subagent create requires the spawning tool call id")
	}
	detail := delegationDetail(task, typeName, name, mode)
	parentCfg := parent.configSnapshot()
	sub := deriveNamedFromConfig(parentCfg, name, parentToolCallID, &detail)
	if resolved != nil {
		if resolved.FormattedPrompt != "" {
			prompt = resolved.FormattedPrompt + "\n\n" + prompt
		}
		if resolved.Model != "" {
			sub.SetProvider(parentCfg.Provider, resolved.Model)
		}
	}
	if mode == "fork" {
		sub.Cfg.Messages = truncateToLastCompleteBoundary(parentCfg.Messages)
		sub.Cfg.SystemPrompt = parentCfg.SystemPrompt
	}

	switch mode {
	case "sync":
		return t.runSync(ctx, sub, prompt, name, typeName, timeout)
	case "fork":
		return t.runFork(ctx, sub, prompt, name, typeName, parentInbox, parentCfg.Logger)
	default:
		return t.runAsync(ctx, sub, prompt, name, typeName, parentInbox, parentCfg.Logger)
	}
}

func delegationFromToolCall(toolName string, args any) (ext.DelegationDetail, bool) {
	if toolName != "subagent" {
		return ext.DelegationDetail{}, false
	}
	values, ok := args.(map[string]any)
	if !ok {
		return ext.DelegationDetail{}, false
	}
	if action, _ := values["action"].(string); action != "" && action != "create" {
		return ext.DelegationDetail{}, false
	}
	task, _ := values["prompt"].(string)
	if strings.TrimSpace(task) == "" {
		return ext.DelegationDetail{}, false
	}
	name, _ := values["name"].(string)
	typeName, _ := values["type"].(string)
	mode, _ := values["mode"].(string)
	return delegationDetail(task, typeName, name, mode), true
}

func delegationDetail(task, typeName, name, mode string) ext.DelegationDetail {
	detail := ext.DelegationDetail{
		Task:      task,
		AgentName: name,
		AgentType: typeName,
	}
	switch mode {
	case "sync":
		detail.RunMode = ext.DelegationRunForeground
		detail.ContextMode = ext.DelegationContextFresh
	case "async":
		detail.RunMode = ext.DelegationRunBackground
		detail.ContextMode = ext.DelegationContextFresh
	case "fork":
		detail.RunMode = ext.DelegationRunBackground
		detail.ContextMode = ext.DelegationContextFork
	}
	return detail
}

func (t *SubAgentTool) runSync(ctx context.Context, sub *Agent, prompt, name, typeName, timeoutStr string) (string, error) {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if timeoutStr != "" {
		timeout, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return "", fmt.Errorf("invalid timeout %q: %w", timeoutStr, err)
		}
		subCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	r, err := runDerivedSession(subCtx, sub, prompt)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Sprintf("subagent %q timed out after %s", name, timeoutStr), nil
		}
		return fmt.Sprintf("subagent %q failed: %s", name, err), nil
	}
	return fmt.Sprintf("<subagent_result name=%q type=%q status=\"completed\">\n%s\n</subagent_result>", name, typeName, resultOutput(r)), nil
}

func (t *SubAgentTool) runAsync(ctx context.Context, sub *Agent, prompt, name, typeName string, parentInbox inbox.Inbox, logger telemetry.Logger) (string, error) {
	subCtx, cancel := context.WithCancel(ctx)
	sub.Cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	t.track(name, typeName, "async", cancel, sub.Cfg.Inbox)
	producer := parentInbox.RegisterProducer("subagent:" + name)

	go func() {
		defer producer.Done()
		defer t.untrack(name)
		defer cancel()
		r, err := runDerivedSession(subCtx, sub, prompt)
		t.pushCompletion(parentInbox, logger, name, typeName, r, err)
	}()

	return fmt.Sprintf("Started subagent %q (mode=async, type=%s). Will notify on completion.", name, typeName), nil
}

func (t *SubAgentTool) runFork(ctx context.Context, sub *Agent, directive, name, typeName string, parentInbox inbox.Inbox, logger telemetry.Logger) (string, error) {
	subCtx, cancel := context.WithCancel(ctx)
	sub.Cfg.Inbox = inbox.NewBuffered(SubInboxCapacity)
	t.track(name, typeName, "fork", cancel, sub.Cfg.Inbox)
	producer := parentInbox.RegisterProducer("subagent:" + name)

	go func() {
		defer producer.Done()
		defer t.untrack(name)
		defer cancel()
		r, err := runDerivedSession(subCtx, sub, directive)
		t.pushCompletion(parentInbox, logger, name, typeName, r, err)
	}()

	return fmt.Sprintf("Started subagent %q (mode=fork, type=%s). Inherits parent context. Will notify on completion.", name, typeName), nil
}

func runDerivedSession(ctx context.Context, sub *Agent, prompt string) (*Result, error) {
	turnID := randomID()
	sub.beginSession()
	emitter := sub.configSnapshot().emitter.turn(turnID)
	emitter.turnStart()
	result, err := sub.Run(ctx, TextInput(prompt), WithTurnID(turnID))
	stop := StopReasonError
	usage := Usage{}
	contextTokens := 0
	if result != nil {
		stop = result.Stop
		usage = result.TotalUsage
		contextTokens = result.ContextTokens
	} else if errors.Is(err, context.Canceled) {
		stop = StopReasonCanceled
	}
	emitter.turnEnd(stop, usage, contextTokens, err)
	reason := string(stop)
	if reason == "" {
		reason = string(StopReasonCompleted)
	}
	sub.endSession(reason)
	return result, err
}

func (t *SubAgentTool) pushCompletion(parentInbox inbox.Inbox, logger telemetry.Logger, name, typeName string, r *Result, err error) {
	status, content := subagentCompletion(r, err)

	msg := inbox.NewMessage(inbox.OriginSystem, "user",
		fmt.Sprintf("<subagent_completion name=%q type=%q status=%q>\n%s\n</subagent_completion>", name, typeName, status, content))
	msg.Meta = map[string]any{"subagent": name, "type": typeName, "status": status}
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

func (t *SubAgentTool) sendMessage(name, message string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required for message action")
	}
	if message == "" {
		return "", fmt.Errorf("message is required")
	}
	t.mu.Lock()
	info, ok := t.running[name]
	t.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no running subagent named %q", name)
	}
	msg := inbox.NewMessage(inbox.OriginUser, "user", message)
	if err := info.Inbox.Push(msg); err != nil {
		return fmt.Sprintf("Subagent %q inbox: %s, message dropped.", name, err), nil
	}
	return fmt.Sprintf("Message sent to subagent %q.", name), nil
}

func (t *SubAgentTool) list() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.running) == 0 {
		return "No subagents running."
	}
	var sb strings.Builder
	sb.WriteString("Running subagents:\n")
	for name, info := range t.running {
		elapsed := time.Since(info.StartedAt).Round(time.Second)
		sb.WriteString(fmt.Sprintf("  - %s (type=%s, mode=%s, running %s)\n", name, info.Type, info.Mode, elapsed))
	}
	return sb.String()
}

func (t *SubAgentTool) kill(name string) (string, error) {
	t.mu.Lock()
	info, ok := t.running[name]
	t.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no running subagent named %q", name)
	}
	info.Cancel()
	return fmt.Sprintf("Subagent %q canceled.", name), nil
}

func (t *SubAgentTool) track(name, typeName, mode string, cancel context.CancelFunc, ib inbox.Inbox) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running[name] = &subAgentInfo{
		Name:      name,
		Type:      typeName,
		Mode:      mode,
		StartedAt: time.Now(),
		Cancel:    cancel,
		Inbox:     ib,
	}
}

func (t *SubAgentTool) untrack(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.running, name)
}

func (t *SubAgentTool) uniqueName(base string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.running[base]; !exists {
		return base
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return base + "-" + hex.EncodeToString(b)
}

func truncateToLastCompleteBoundary(messages []ChatMessage) []ChatMessage {
	out := append([]ChatMessage(nil), messages...)
	for i := len(out) - 1; i >= 0; i-- {
		msg := out[i]
		if msg.Role == "tool" || msg.Role == "user" {
			return out[:i+1]
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) == 0 {
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
