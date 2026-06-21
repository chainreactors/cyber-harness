package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
)

type TmuxCommand struct {
	manager *tmux.Manager
}

func NewTmuxCommand(mgr *tmux.Manager) *TmuxCommand {
	return &TmuxCommand{manager: mgr}
}

func (t *TmuxCommand) Name() string { return "tmux" }

func (t *TmuxCommand) Usage() string {
	return `tmux - PTY session manager

  new-session [-d] [-s name] [--timeout duration] "command"
      Create session. -d detached (background). -s session name.

  ls / list-sessions
      List all sessions.

  send-keys -t <id> "text" [Enter]
      Send keystrokes. Append Enter/C-m to send newline.

  capture-pane -t <id> [-p] [-n lines] [-c bytes] [--full]
      Show new output since last read (default incremental).
      -n N: last N lines. -c N: last N bytes. --full: entire buffer.

  kill-session -t <id>
      Terminate session.

  wait-for -t <id> [--timeout duration]
      Block until session completes.`
}

func (t *TmuxCommand) Execute(ctx context.Context, args []string) error {
	var result string
	var err error
	if len(args) == 0 {
		result = t.Usage()
	} else {
		switch args[0] {
		case "new", "new-session":
			result, err = t.cmdNewSession(ctx, args[1:])
		case "ls", "list-sessions":
			result, err = t.cmdListSessions()
		case "send", "send-keys":
			result, err = t.cmdSendKeys(args[1:])
		case "peek", "capture-pane":
			result, err = t.cmdCapturePane(args[1:])
		case "kill", "kill-session":
			result, err = t.cmdKillSession(args[1:])
		case "wait", "wait-for":
			result, err = t.cmdWaitFor(ctx, args[1:])
		default:
			result, err = t.cmdImplicitNewSession(args)
		}
	}
	if err != nil {
		return err
	}
	if result != "" {
		fmt.Fprint(Output, result)
	}
	return nil
}

func (t *TmuxCommand) cmdImplicitNewSession(args []string) (string, error) {
	cmdLine := strings.Join(args, " ")
	info, err := t.createSession(cmdLine, "", tmux.DefaultTimeout)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: 1 windows (created %s) [detached]\nUse `tmux capture-pane -t %s` to check new output.",
		info.ID, info.StartedAt.Format("Mon Jan 2 15:04:05 2006"), info.ID), nil
}

// new-session [-d] [-s name] [--timeout 30m] "command args..."
func (t *TmuxCommand) cmdNewSession(ctx context.Context, args []string) (string, error) {
	var detached bool
	var name, timeoutStr string
	var cmdParts []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-d":
			detached = true
		case "-s":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				timeoutStr = args[i]
			}
		default:
			cmdParts = append(cmdParts, args[i])
		}
	}

	if len(cmdParts) == 0 {
		return "", fmt.Errorf("tmux new-session: missing command")
	}

	cmdLine := strings.Join(cmdParts, " ")
	timeout := tmux.DefaultTimeout
	if timeoutStr != "" {
		d, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return "", fmt.Errorf("tmux new-session: invalid timeout %q: %w", timeoutStr, err)
		}
		timeout = d
	}

	info, err := t.createSession(cmdLine, name, timeout)
	if err != nil {
		return "", err
	}

	if detached {
		return fmt.Sprintf("%s: 1 windows (created %s) [detached]\nUse `tmux capture-pane -t %s` to check new output.",
			info.ID, info.StartedAt.Format("Mon Jan 2 15:04:05 2006"), info.ID), nil
	}

	result, err := t.manager.Wait(ctx, info.ID, timeout)
	if err != nil {
		return "", err
	}
	output := t.manager.PeekOrEmpty(info.ID, 50)
	if result.ExitCode != 0 {
		output += fmt.Sprintf("\n[exit code: %d]", result.ExitCode)
	}
	return output, nil
}

func (t *TmuxCommand) createSession(cmdLine, name string, timeout time.Duration) (tmux.Info, error) {
	return t.manager.RunCommand(cmdLine, tmux.RunOpts{
		Name:    name,
		Timeout: timeout,
	})
}

// ls / list-sessions
func (t *TmuxCommand) cmdListSessions() (string, error) {
	items := t.manager.List()
	if len(items) == 0 {
		return "no server running on this host", nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })

	var sb strings.Builder
	for _, it := range items {
		var elapsed time.Duration
		if it.State == tmux.StateRunning {
			elapsed = time.Since(it.StartedAt).Round(time.Second)
		} else {
			elapsed = it.EndedAt.Sub(it.StartedAt).Round(time.Second)
		}
		cmd := truncate.Clip(it.Command, 50)
		// tmux style: "session_name: 1 windows (created ...) [state]"
		fmt.Fprintf(&sb, "%s (%s): %s [%s %s] %s\n",
			it.ID, it.Name, cmd, it.State, elapsed, it.StartedAt.Format("Mon Jan 2 15:04:05 2006"))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// send-keys -t <id> "text" [Enter] [C-m] [C-c]
func (t *TmuxCommand) cmdSendKeys(args []string) (string, error) {
	id, rest := parseTarget(args)
	if id == "" {
		return "", fmt.Errorf("tmux send-keys: -t <id> required")
	}
	if len(rest) == 0 {
		return "", fmt.Errorf("tmux send-keys: keys required")
	}

	var buf strings.Builder
	for _, key := range rest {
		switch key {
		case "Enter", "C-m":
			buf.WriteByte('\n')
		case "C-c":
			buf.WriteByte(0x03)
		case "C-d":
			buf.WriteByte(0x04)
		case "C-z":
			buf.WriteByte(0x1a)
		case "Escape":
			buf.WriteByte(0x1b)
		case "Space":
			buf.WriteByte(' ')
		case "Tab":
			buf.WriteByte('\t')
		case "BSpace":
			buf.WriteByte(0x7f)
		default:
			buf.WriteString(key)
		}
	}

	data := buf.String()
	if err := t.manager.Write(id, []byte(data)); err != nil {
		return "", err
	}
	return fmt.Sprintf("sent %d bytes to %s", len(data), id), nil
}

// capture-pane -t <id> [-p] [-n lines] [-c bytes] [--full]
func (t *TmuxCommand) cmdCapturePane(args []string) (string, error) {
	id, rest := parseTarget(args)
	if id == "" {
		return "", fmt.Errorf("tmux capture-pane: -t <id> required")
	}

	lines := 0
	bytes := 0
	full := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "-p":
		case "--full":
			full = true
		case "--new":
		case "-n":
			if i+1 < len(rest) {
				i++
				_, _ = fmt.Sscanf(rest[i], "%d", &lines)
			}
		case "-c", "--bytes":
			if i+1 < len(rest) {
				i++
				_, _ = fmt.Sscanf(rest[i], "%d", &bytes)
			}
		}
	}

	if bytes > 0 {
		output, err := t.manager.PeekBytes(id, bytes)
		if err != nil {
			return "", err
		}
		if output == "" {
			return "(no output yet)", nil
		}
		return output, nil
	}

	if lines > 0 || full {
		if lines <= 0 {
			lines = 30
		}
		output, err := t.manager.Peek(id, lines)
		if err != nil {
			return "", err
		}
		if output == "" {
			return "(no output yet)", nil
		}
		return output, nil
	}

	output, more, err := t.manager.PeekNew(id, 0)
	if err != nil {
		return "", err
	}
	if output == "" {
		return "(no new output since last read; use --full or -n <lines> to re-read)", nil
	}
	if more {
		output += "\n\n[more output available; run `tmux capture-pane -t " + id + "` again]"
	}
	return output, nil
}

// kill-session -t <id>
func (t *TmuxCommand) cmdKillSession(args []string) (string, error) {
	id, _ := parseTarget(args)
	if id == "" {
		return "", fmt.Errorf("tmux kill-session: -t <id> required")
	}
	if err := t.manager.Kill(id); err != nil {
		return "", err
	}
	return fmt.Sprintf("killed session %s", id), nil
}

// wait-for -t <id> [--timeout 60s]
func (t *TmuxCommand) cmdWaitFor(ctx context.Context, args []string) (string, error) {
	id, rest := parseTarget(args)
	if id == "" {
		return "", fmt.Errorf("tmux wait-for: -t <id> required")
	}

	timeout := 60 * time.Second
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--timeout" && i+1 < len(rest) {
			i++
			d, err := time.ParseDuration(rest[i])
			if err != nil {
				return "", fmt.Errorf("tmux wait-for: invalid timeout: %w", err)
			}
			timeout = d
		}
	}

	info, err := t.manager.Wait(ctx, id, timeout)
	if err != nil {
		return "", err
	}
	if info.State == tmux.StateRunning {
		return fmt.Sprintf("%s: still running (%s elapsed)",
			info.ID, time.Since(info.StartedAt).Round(time.Second)), nil
	}
	duration := info.EndedAt.Sub(info.StartedAt).Round(time.Second)
	return fmt.Sprintf("%s: %s (exit %d, %s)", info.ID, info.State, info.ExitCode, duration), nil
}

func parseTarget(args []string) (string, []string) {
	var id string
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-t" && i+1 < len(args) {
			i++
			id = args[i]
		} else {
			rest = append(rest, args[i])
		}
	}
	return id, rest
}
