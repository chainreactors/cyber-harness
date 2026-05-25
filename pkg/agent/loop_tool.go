package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/command"
)

type LoopTool struct {
	scheduler *LoopScheduler
}

func NewLoopTool(scheduler *LoopScheduler) *LoopTool {
	return &LoopTool{scheduler: scheduler}
}

func (t *LoopTool) Name() string { return "loop" }

func (t *LoopTool) Description() string {
	return "Manage recurring scheduled tasks. Actions: create (schedule a periodic task), list (show active loops), delete (remove a loop by name). Loop prompts fire at fixed intervals and are processed as system messages."
}

type loopArgs struct {
	Action    string `json:"action"              jsonschema:"description=Action to perform,enum=create,enum=list,enum=delete"`
	Name      string `json:"name,omitempty"       jsonschema:"description=Loop name (required for create/delete)"`
	Interval  string `json:"interval,omitempty"   jsonschema:"description=Fire interval e.g. 30s 5m 1h (required for create)"`
	Prompt    string `json:"prompt,omitempty"     jsonschema:"description=Prompt to execute on each fire (required for create)"`
	Immediate bool   `json:"immediate,omitempty"  jsonschema:"description=Fire once immediately on creation"`
}

func (t *LoopTool) Definition() provider.ToolDefinition {
	return command.ToolDef("loop", t.Description(), loopArgs{})
}

func (t *LoopTool) Execute(ctx context.Context, arguments string) (command.ToolResult, error) {
	args, err := command.ParseArgs[loopArgs](arguments)
	if err != nil {
		return command.ToolResult{}, err
	}

	switch args.Action {
	case "create":
		return t.create(ctx, args)
	case "list":
		return t.list()
	case "delete":
		return t.delete(args)
	default:
		return command.ErrorResult(fmt.Sprintf("unknown action: %s", args.Action)), nil
	}
}

func (t *LoopTool) create(ctx context.Context, args loopArgs) (command.ToolResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return command.ToolResult{}, fmt.Errorf("name is required for create")
	}
	if strings.TrimSpace(args.Interval) == "" {
		return command.ToolResult{}, fmt.Errorf("interval is required for create")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return command.ToolResult{}, fmt.Errorf("prompt is required for create")
	}

	interval, err := time.ParseDuration(args.Interval)
	if err != nil {
		return command.ToolResult{}, fmt.Errorf("invalid interval %q: %w", args.Interval, err)
	}

	entry := LoopEntry{
		Name:      args.Name,
		Interval:  interval,
		Prompt:    args.Prompt,
		Mode:      ModeInbox,
		Immediate: args.Immediate,
	}
	if err := t.scheduler.Add(ctx, entry); err != nil {
		return command.ToolResult{}, err
	}

	msg := fmt.Sprintf("Loop %q created: fires every %s", args.Name, interval)
	if args.Immediate {
		msg += " (first fire: now)"
	}
	return command.TextResult(msg), nil
}

func (t *LoopTool) list() (command.ToolResult, error) {
	loops := t.scheduler.List()
	if len(loops) == 0 {
		return command.TextResult("No active loops."), nil
	}
	var sb strings.Builder
	sb.WriteString("Active loops:\n")
	for _, l := range loops {
		sb.WriteString(fmt.Sprintf("  - %s: interval=%s fires=%d", l.Name, l.Interval, l.FireCount))
		if !l.LastFired.IsZero() {
			sb.WriteString(fmt.Sprintf(" last=%s", time.Since(l.LastFired).Round(time.Second)))
		}
		sb.WriteString(fmt.Sprintf(" prompt=%q\n", truncatePrompt(l.Prompt, 60)))
	}
	return command.TextResult(sb.String()), nil
}

func (t *LoopTool) delete(args loopArgs) (command.ToolResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return command.ToolResult{}, fmt.Errorf("name is required for delete")
	}
	if err := t.scheduler.Remove(args.Name); err != nil {
		return command.ToolResult{}, err
	}
	return command.TextResult(fmt.Sprintf("Loop %q deleted.", args.Name)), nil
}

func truncatePrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
