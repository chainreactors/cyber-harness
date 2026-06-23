package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
)

type LoopTool struct {
	scheduler *LoopScheduler
}

func NewLoopTool(scheduler *LoopScheduler) *LoopTool {
	return &LoopTool{scheduler: scheduler}
}

func (t *LoopTool) Name() string        { return "loop" }
func (t *LoopTool) Description() string {
	return "Manage recurring scheduled tasks. Use action=create to register a periodic task, action=list to view active loops, action=remove to cancel one."
}

type LoopToolArgs struct {
	Action   string `json:"action"              jsonschema:"description=create: register a new loop. list: show active loops. remove: cancel a loop by name.,enum=create,enum=list,enum=remove"`
	Name     string `json:"name,omitempty"       jsonschema:"description=Unique name for the loop (required for create and remove)"`
	Prompt   string `json:"prompt,omitempty"     jsonschema:"description=The prompt or instruction to execute each interval (required for create)"`
	Interval string `json:"interval,omitempty"   jsonschema:"description=Repeat interval as a Go duration string e.g. 30s 5m 1h (required for create; minimum 10s)"`
}

func (t *LoopTool) Definition() commands.ToolDefinition {
	return commands.ToolDef(t.Name(), t.Description(), LoopToolArgs{})
}

func (t *LoopTool) Execute(ctx context.Context, arguments string) (commands.ToolResult, error) {
	args, err := commands.ParseArgs[LoopToolArgs](arguments)
	if err != nil {
		return commands.ToolResult{}, err
	}

	switch args.Action {
	case "create":
		return t.create(ctx, args)
	case "list":
		return t.list()
	case "remove":
		return t.remove(args)
	default:
		return commands.ToolResult{IsError: true}, fmt.Errorf("unknown action %q (use create, list, or remove)", args.Action)
	}
}

func (t *LoopTool) create(ctx context.Context, args LoopToolArgs) (commands.ToolResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return commands.ToolResult{IsError: true}, fmt.Errorf("name is required for create")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return commands.ToolResult{IsError: true}, fmt.Errorf("prompt is required for create")
	}
	if strings.TrimSpace(args.Interval) == "" {
		return commands.ToolResult{IsError: true}, fmt.Errorf("interval is required for create")
	}
	interval, err := time.ParseDuration(args.Interval)
	if err != nil {
		return commands.ToolResult{IsError: true}, fmt.Errorf("invalid interval %q: %w", args.Interval, err)
	}

	entry := LoopEntry{
		Name:     args.Name,
		Interval: interval,
		Mode:     ModeInbox,
		Prompt:   args.Prompt,
	}
	if err := t.scheduler.Add(ctx, entry); err != nil {
		return commands.ToolResult{IsError: true}, err
	}
	return commands.TextResult(fmt.Sprintf("Loop %q created: every %s", args.Name, interval)), nil
}

func (t *LoopTool) list() (commands.ToolResult, error) {
	loops := t.scheduler.List()
	if len(loops) == 0 {
		return commands.TextResult("No active loops."), nil
	}
	var b strings.Builder
	for _, l := range loops {
		fmt.Fprintf(&b, "- %s  interval=%s  fires=%d", l.Name, l.Interval, l.FireCount)
		if !l.LastFired.IsZero() {
			fmt.Fprintf(&b, "  last=%s", l.LastFired.Format(time.RFC3339))
		}
		if l.Prompt != "" {
			fmt.Fprintf(&b, "  prompt=%q", l.Prompt)
		}
		b.WriteByte('\n')
	}
	return commands.TextResult(b.String()), nil
}

func (t *LoopTool) remove(args LoopToolArgs) (commands.ToolResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return commands.ToolResult{IsError: true}, fmt.Errorf("name is required for remove")
	}
	if err := t.scheduler.Remove(args.Name); err != nil {
		return commands.ToolResult{IsError: true}, err
	}
	return commands.TextResult(fmt.Sprintf("Loop %q removed.", args.Name)), nil
}
