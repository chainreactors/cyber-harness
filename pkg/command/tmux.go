package command

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/task"
)

type TmuxCommand struct {
	manager  *task.Manager
	registry *CommandRegistry
	workDir  string
	selfBin  string
	proxy    string
}

func NewTmuxCommand(mgr *task.Manager, registry *CommandRegistry) *TmuxCommand {
	return &TmuxCommand{manager: mgr, registry: registry}
}

func (t *TmuxCommand) Name() string     { return "tmux" }
func (t *TmuxCommand) InProcess()       {}
func (t *TmuxCommand) SetWorkDir(d string) { t.workDir = d }
func (t *TmuxCommand) SetSelfBin(b string) { t.selfBin = b }
func (t *TmuxCommand) SetProxy(p string)   { t.proxy = p }

func (t *TmuxCommand) Usage() string {
	return `tmux - PTY session manager

  new-session [-d] [-s name] [--timeout duration] "command"
      Create session. -d detached (background). -s session name.

  ls / list-sessions
      List all sessions.

  send-keys -t <id> "text" [Enter]
      Send keystrokes. Append Enter/C-m to send newline.

  capture-pane -t <id> [-p] [-n lines] [--new]
      Show session output. --new for incremental since last read.

  kill-session -t <id>
      Terminate session.

  wait-for -t <id> [--timeout duration]
      Block until session completes.`
}

func (t *TmuxCommand) Execute(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return t.Usage(), nil
	}
	switch args[0] {
	case "new", "new-session":
		return t.cmdNewSession(ctx, args[1:])
	case "ls", "list-sessions":
		return t.cmdListSessions()
	case "send", "send-keys":
		return t.cmdSendKeys(args[1:])
	case "peek", "capture-pane":
		return t.cmdCapturePane(args[1:])
	case "kill", "kill-session":
		return t.cmdKillSession(args[1:])
	case "wait", "wait-for":
		return t.cmdWaitFor(ctx, args[1:])
	default:
		return "", fmt.Errorf("tmux: unknown subcommand %q\n%s", args[0], t.Usage())
	}
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
	timeout := task.DefaultTimeout
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
		return fmt.Sprintf("%s: 1 windows (created %s) [detached]\nUse `tmux capture-pane -t %s` to inspect output.",
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

func (t *TmuxCommand) createSession(cmdLine, name string, timeout time.Duration) (task.Info, error) {
	token := firstCommandToken(cmdLine)
	isPseudo := false
	if t.registry != nil && token != "tmux" {
		if cmd, ok := t.registry.Get(token); ok {
			if _, inProc := cmd.(InProcessCommand); !inProc {
				isPseudo = true
			}
		}
	}

	if isPseudo {
		tokens, err := SplitCommandLine(cmdLine)
		if err != nil {
			return task.Info{}, fmt.Errorf("tmux new-session: %w", err)
		}
		if len(tokens) > 1 {
			if _, valErr := stripShellSyntax(tokens[1:]); valErr != nil {
				return task.Info{}, valErr
			}
		}
		subArgs := append(tokens, "--no-color")
		self := t.selfBin
		if self == "" {
			var err error
			self, err = os.Executable()
			if err != nil {
				return task.Info{}, fmt.Errorf("tmux new-session: resolve self: %w", err)
			}
		}
		return t.manager.CreateCmd(t.workDir, self, subArgs, name, timeout, t.buildEnv(), "")
	}

	return t.manager.Create(t.workDir, cmdLine, name, timeout, nil, "")
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
		if it.State == task.StateRunning {
			elapsed = time.Since(it.StartedAt).Round(time.Second)
		} else {
			elapsed = it.EndedAt.Sub(it.StartedAt).Round(time.Second)
		}
		cmd := it.Command
		if len(cmd) > 50 {
			cmd = cmd[:47] + "..."
		}
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

// capture-pane -t <id> [-p] [-n lines] [--new]
func (t *TmuxCommand) cmdCapturePane(args []string) (string, error) {
	id, rest := parseTarget(args)
	if id == "" {
		return "", fmt.Errorf("tmux capture-pane: -t <id> required")
	}

	lines := 30
	incremental := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "-p":
			// -p means print to stdout, which is our default behavior
		case "--new":
			incremental = true
		case "-n":
			if i+1 < len(rest) {
				i++
				_, _ = fmt.Sscanf(rest[i], "%d", &lines)
			}
		}
	}

	if incremental {
		output, more, err := t.manager.PeekNew(id, 0)
		if err != nil {
			return "", err
		}
		if output == "" {
			return "(no new output)", nil
		}
		if more {
			output += "\n\n[more output available; run `tmux capture-pane -t " + id + " --new` again]"
		}
		return output, nil
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
	if info.State == task.StateRunning {
		return fmt.Sprintf("%s: still running (%s elapsed)",
			info.ID, time.Since(info.StartedAt).Round(time.Second)), nil
	}
	duration := info.EndedAt.Sub(info.StartedAt).Round(time.Second)
	return fmt.Sprintf("%s: %s (exit %d, %s)", info.ID, info.State, info.ExitCode, duration), nil
}

func (t *TmuxCommand) buildEnv() []string {
	var env []string
	if t.proxy != "" {
		for _, k := range []string{"ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
			env = append(env, k+"="+t.proxy)
		}
	}
	env = append(env, "AISCAN_PARENT=1")
	return env
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
