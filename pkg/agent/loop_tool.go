package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
)

// LoopCommand is a pseudo-command invoked via bash:
//
//	bash(command="loop '*/5 * * * *' check scan progress")
//	bash(command="loop 30s check status")
//	bash(command="loop list")
//	bash(command="loop stop loop-a1b2c3d4")
type LoopCommand struct {
	scheduler *LoopScheduler
}

func NewLoopCommand(scheduler *LoopScheduler) *LoopCommand {
	return &LoopCommand{scheduler: scheduler}
}

func (c *LoopCommand) Name() string { return "loop" }

func (c *LoopCommand) Usage() string {
	return `loop — recurring task scheduler

Usage:
  loop <schedule> <prompt>    create a recurring task
  loop list                   show all active loops
  loop stop <name>            stop a loop by name
  loop stop-all               stop all loops

Schedule formats:
  cron       "*/5 * * * *"   standard 5-field cron expression
  duration   30s, 5m, 1h     Go duration shorthand (minimum 10s)

Examples:
  loop "*/5 * * * *" check scan progress    every 5 minutes
  loop "0 */2 * * *" review findings        every 2 hours
  loop "30 9 * * 1-5" daily standup check   9:30 on weekdays
  loop 30s check status                     every 30 seconds
  loop 5m monitor targets                   every 5 minutes`
}

func (c *LoopCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(commands.Output, c.Usage()+"\n")
		return nil
	}

	switch strings.ToLower(args[0]) {
	case "list", "ls":
		return c.list()
	case "stop", "rm", "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: loop stop <name>")
		}
		return c.stop(args[1])
	case "stop-all":
		c.scheduler.Stop()
		_, _ = fmt.Fprint(commands.Output, "All loops stopped.\n")
		return nil
	default:
		return c.create(ctx, args)
	}
}

func (c *LoopCommand) create(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: loop <schedule> <prompt>")
	}

	entry := LoopEntry{Mode: ModeInbox}

	// try cron first (5 space-separated fields), then duration
	if cron, rest, ok := tryCronPrefix(args); ok {
		entry.Cron = cron
		entry.Prompt = strings.Join(rest, " ")
	} else if dur, err := time.ParseDuration(args[0]); err == nil {
		entry.Interval = dur
		entry.Prompt = strings.Join(args[1:], " ")
	} else {
		return fmt.Errorf("invalid schedule %q: expected cron expression (5 fields) or duration (30s/5m/1h)", args[0])
	}

	if strings.TrimSpace(entry.Prompt) == "" {
		return fmt.Errorf("usage: loop <schedule> <prompt>")
	}

	name, err := c.scheduler.Add(ctx, entry)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(commands.Output, "Loop %q created: %s\n", name, entry.Schedule())
	return nil
}

// tryCronPrefix attempts to parse the first 5 args as a cron expression.
// Returns the parsed expression, remaining args, and whether it succeeded.
func tryCronPrefix(args []string) (*CronExpr, []string, bool) {
	if len(args) >= 2 && strings.Contains(args[0], " ") {
		if cron, err := ParseCron(args[0]); err == nil {
			return cron, args[1:], true
		}
	}
	if len(args) < 6 {
		return nil, nil, false
	}
	expr := strings.Join(args[:5], " ")
	cron, err := ParseCron(expr)
	if err != nil {
		return nil, nil, false
	}
	return cron, args[5:], true
}

func (c *LoopCommand) list() error {
	loops := c.scheduler.List()
	if len(loops) == 0 {
		_, _ = fmt.Fprint(commands.Output, "No active loops.\n")
		return nil
	}
	for _, l := range loops {
		line := fmt.Sprintf("- %s  schedule=%s  fires=%d", l.Name, l.Schedule, l.FireCount)
		if !l.LastFired.IsZero() {
			line += fmt.Sprintf("  last=%s", l.LastFired.Format(time.RFC3339))
		}
		if l.Prompt != "" {
			line += fmt.Sprintf("  prompt=%q", l.Prompt)
		}
		_, _ = fmt.Fprintln(commands.Output, line)
	}
	return nil
}

func (c *LoopCommand) stop(name string) error {
	if err := c.scheduler.Remove(name); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(commands.Output, "Loop %q stopped.\n", name)
	return nil
}
