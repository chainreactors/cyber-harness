package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/skills"
)


type SubAgentConfig struct {
	ParentConfig agent.Config
	ParentInbox  inbox.Inbox
	SkillStore   *skills.Store
}

type subAgentInfo struct {
	Name      string
	Type      string
	Mode      string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Inbox     inbox.Inbox
}

type SubAgentTool struct {
	cfg     SubAgentConfig
	mu      sync.Mutex
	running map[string]*subAgentInfo
}

func NewSubAgentTool(cfg SubAgentConfig) *SubAgentTool {
	return &SubAgentTool{
		cfg:     cfg,
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

func (t *SubAgentTool) Definition() provider.ToolDefinition {
	return command.ToolDef(t.Name(), t.Description(), SubAgentArgs{})
}

func (t *SubAgentTool) Execute(ctx context.Context, arguments string) (command.ToolResult, error) {
	return t.execute(ctx, arguments, nil)
}

func (t *SubAgentTool) ExecuteWithContext(ctx context.Context, arguments string, toolCtx command.ToolContext) (command.ToolResult, error) {
	return t.execute(ctx, arguments, &toolCtx)
}

func (t *SubAgentTool) execute(ctx context.Context, arguments string, toolCtx *command.ToolContext) (command.ToolResult, error) {
	args, err := command.ParseArgs[SubAgentArgs](arguments)
	if err != nil {
		return command.ToolResult{}, err
	}

	switch args.Action {
	case "list":
		return command.TextResult(t.list()), nil
	case "kill":
		output, err := t.kill(args.Name)
		if err != nil {
			return command.ToolResult{}, err
		}
		return command.TextResult(output), nil
	case "message":
		output, err := t.sendMessage(args.Name, args.Message)
		if err != nil {
			return command.ToolResult{}, err
		}
		return command.TextResult(output), nil
	case "", "create":
		output, err := t.create(ctx, args.Prompt, args.Type, args.Name, args.Mode, args.Timeout, toolCtx)
		if err != nil {
			return command.ToolResult{}, err
		}
		return command.TextResult(output), nil
	default:
		return command.ToolResult{}, fmt.Errorf("unknown action: %s", args.Action)
	}
}

func (t *SubAgentTool) create(ctx context.Context, prompt, typeName, name, mode, timeout string, toolCtx *command.ToolContext) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}

	var skill *skills.Skill
	if typeName != "" && t.cfg.SkillStore != nil {
		s, ok := t.cfg.SkillStore.ByName(typeName)
		if !ok {
			return "", fmt.Errorf("agent type %q not found", typeName)
		}
		if !s.Agent {
			return "", fmt.Errorf("skill %q is not configured as an agent type", typeName)
		}
		skill = &s
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
		if skill != nil && !skill.AgentBackground {
			mode = "sync"
		}
	}

	cfg := t.cfg.ParentConfig.DeriveChild()
	if skill != nil {
		prompt = skills.FormatInvocation(*skill, prompt)
		if skill.AgentModel != "" {
			cfg = cfg.WithModel(skill.AgentModel)
		}
	}

	switch mode {
	case "sync":
		return t.runSync(ctx, cfg, prompt, name, typeName, toolCtx, timeout)
	case "fork":
		return t.runFork(ctx, cfg, prompt, name, typeName, toolCtx)
	default: // async
		return t.runAsync(ctx, cfg, prompt, name, typeName)
	}
}

func (t *SubAgentTool) runSync(ctx context.Context, cfg agent.Config, prompt, name, typeName string, toolCtx *command.ToolContext, timeoutStr string) (string, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if timeoutStr != "" {
		timeout, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return "", fmt.Errorf("invalid timeout %q: %w", timeoutStr, err)
		}
		childCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	r, err := cfg.Run(childCtx, prompt)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Sprintf("subagent %q timed out after %s", name, timeoutStr), nil
		}
		return fmt.Sprintf("subagent %q failed: %s", name, err), nil
	}
	output := ""
	if r != nil {
		output = r.Output
	}
	return fmt.Sprintf("<subagent_result name=%q type=%q status=\"completed\">\n%s\n</subagent_result>", name, typeName, output), nil
}

func (t *SubAgentTool) runAsync(ctx context.Context, cfg agent.Config, prompt, name, typeName string) (string, error) {
	childCtx, cancel := context.WithCancel(ctx)
	childIb := inbox.NewBuffered(16)
	t.track(name, typeName, "async", cancel, childIb)
	producer := t.cfg.ParentInbox.RegisterProducer("subagent:" + name)

	go func() {
		defer producer.Done()
		defer t.untrack(name)
		defer cancel()
		childCfg := cfg.WithInbox(childIb)
		r, err := childCfg.Run(childCtx, prompt)
		t.pushCompletion(name, typeName, r, err)
	}()

	return fmt.Sprintf("Started subagent %q (mode=async, type=%s). Will notify on completion.", name, typeName), nil
}

func (t *SubAgentTool) runFork(ctx context.Context, cfg agent.Config, directive, name, typeName string, toolCtx *command.ToolContext) (string, error) {
	var parentMessages []provider.ChatMessage
	if toolCtx != nil {
		parentMessages = truncateToLastCompleteBoundary(toolCtx.Messages)
		if toolCtx.SystemPrompt != "" {
			cfg.SystemPrompt = toolCtx.SystemPrompt
		}
	}

	childCtx, cancel := context.WithCancel(ctx)
	childIb := inbox.NewBuffered(16)
	t.track(name, typeName, "fork", cancel, childIb)
	producer := t.cfg.ParentInbox.RegisterProducer("subagent:" + name)

	go func() {
		defer producer.Done()
		defer t.untrack(name)
		defer cancel()
		childCfg := cfg.WithInbox(childIb)
		r, err := childCfg.RunWithContext(childCtx, directive, parentMessages)
		t.pushCompletion(name, typeName, r, err)
	}()

	return fmt.Sprintf("Started subagent %q (mode=fork, type=%s). Inherits parent context. Will notify on completion.", name, typeName), nil
}

func (t *SubAgentTool) pushCompletion(name, typeName string, r *agent.Result, err error) {
	result := ""
	if r != nil {
		result = r.Output
	}
	status := "completed"
	content := result
	if err != nil {
		status = "failed"
		if result != "" {
			content = fmt.Sprintf("Error: %s\n\nPartial output:\n%s", err, result)
		} else {
			content = fmt.Sprintf("Error: %s", err)
		}
	}

	msg := inbox.NewMessage(inbox.OriginSystem, "user",
		fmt.Sprintf("<subagent_completion name=%q type=%q status=%q>\n%s\n</subagent_completion>", name, typeName, status, content))
	msg.Meta = map[string]any{"subagent": name, "type": typeName, "status": status}
	if err := t.cfg.ParentInbox.Push(msg); err != nil {
		t.cfg.ParentConfig.Logger.Warnf("inbox push subagent completion %s: %s", name, err)
	}
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

func (t *SubAgentTool) RunningCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.running)
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
	return fmt.Sprintf("Subagent %q cancelled.", name), nil
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
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, exists := t.running[candidate]; !exists {
			return candidate
		}
	}
}

func truncateToLastCompleteBoundary(messages []provider.ChatMessage) []provider.ChatMessage {
	out := append([]provider.ChatMessage(nil), messages...)
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
