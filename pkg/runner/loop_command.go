package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// LoopCommand is a pseudo-command invoked via bash:
//
//	bash(command="loop '*/5 * * * *' check scan progress")
//	bash(command="loop 30s check status")
//	bash(command="loop list")
//	bash(command="loop stop loop-a1b2c3d4")
type loopCommand struct{}

func newLoopCommand() *loopCommand { return &loopCommand{} }

func (c *loopCommand) Name() string { return "loop" }

func (c *loopCommand) Usage() string {
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

func (c *loopCommand) Run(ctx context.Context, execution *commands.Execution) (any, error) {
	scheduler := agent.LoopSchedulerFromContext(ctx)
	if scheduler == nil {
		return nil, fmt.Errorf("loop scheduler is not configured")
	}
	args := execution.Args
	output := execution.Stdout
	if output == nil {
		output = io.Discard
	}
	if len(args) == 0 {
		_, _ = fmt.Fprint(output, c.Usage()+"\n")
		return nil, nil
	}

	switch strings.ToLower(args[0]) {
	case "list", "ls":
		return nil, c.list(scheduler, output)
	case "stop", "rm", "remove":
		if len(args) < 2 {
			return nil, fmt.Errorf("usage: loop stop <name>")
		}
		return nil, c.stop(scheduler, output, args[1])
	case "stop-all":
		scheduler.Stop()
		_, _ = fmt.Fprint(output, "All loops stopped.\n")
		return nil, nil
	default:
		return nil, c.create(ctx, scheduler, output, args)
	}
}

func (c *loopCommand) create(ctx context.Context, scheduler *agent.LoopScheduler, output io.Writer, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: loop <schedule> <prompt>")
	}

	entry := agent.LoopEntry{Mode: agent.ModeInbox}

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

	name, err := scheduler.Add(ctx, entry)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Loop %q created: %s\n", name, entry.Schedule())
	return nil
}

// tryCronPrefix attempts to parse the first 5 args as a cron expression.
// Returns the parsed expression, remaining args, and whether it succeeded.
func tryCronPrefix(args []string) (*agent.CronExpr, []string, bool) {
	if len(args) >= 2 && strings.Contains(args[0], " ") {
		if cron, err := agent.ParseCron(args[0]); err == nil {
			return cron, args[1:], true
		}
	}
	if len(args) < 6 {
		return nil, nil, false
	}
	expr := strings.Join(args[:5], " ")
	cron, err := agent.ParseCron(expr)
	if err != nil {
		return nil, nil, false
	}
	return cron, args[5:], true
}

func (c *loopCommand) list(scheduler *agent.LoopScheduler, output io.Writer) error {
	loops := scheduler.List()
	if len(loops) == 0 {
		_, _ = fmt.Fprint(output, "No active loops.\n")
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
		_, _ = fmt.Fprintln(output, line)
	}
	return nil
}

func (c *loopCommand) stop(scheduler *agent.LoopScheduler, output io.Writer, name string) error {
	if err := scheduler.Remove(name); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Loop %q stopped.\n", name)
	return nil
}
