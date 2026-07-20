package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
)

const (
	defaultTimeout          = 300
	autoBackgroundThreshold = 15 * time.Second
	streamInterval          = 100 * time.Millisecond
	monitorInterval         = 10 * time.Second
)

// BashExecOptions controls one foreground execution without mutating the
// BashTool defaults. Runner/WebAgent transports use this entry point while the
// agent-facing Execute method keeps its auto-background behavior.
type BashExecOptions struct {
	WorkDir  string
	Env      map[string]string
	Timeout  time.Duration
	OnOutput func([]byte)
}

type BashTool struct {
	workDir        string
	timeout        int
	scannerProxy   string
	tasks          *tmux.Manager
	commandNames   func() []string
	inbox          inbox.Inbox
}

func NewBashTool(workDir string, timeout int) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &BashTool{workDir: workDir, timeout: timeout, tasks: tmux.NewManager()}
}

func (t *BashTool) Manager() *tmux.Manager             { return t.tasks }
func (t *BashTool) SetScannerProxy(proxy string)       { t.scannerProxy = proxy }
func (t *BashTool) SetCommandNames(fn func() []string) { t.commandNames = fn }
func (t *BashTool) SetCommandResolver(fn func(string) (Command, bool)) {
	t.tasks.SetCommands(func(name string) (tmux.Command, bool) { return fn(name) })
}
func (t *BashTool) SetInbox(ib inbox.Inbox) { t.inbox = ib }
func (t *BashTool) Name() string            { return "bash" }
func (t *BashTool) Close()                  { t.tasks.Shutdown() }

func (t *BashTool) WithScannerProxy(proxy string) *BashTool {
	t.scannerProxy = proxy
	return t
}

func (t *BashTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.commandNames != nil {
		if names := t.commandNames(); len(names) > 0 {
			desc += " IMPORTANT: This tool also handles pseudo-commands (" + strings.Join(names, ", ") + "). Pass them as the command parameter."
		}
	}
	return desc
}

type BashArgs struct {
	Command string `json:"command" jsonschema:"description=The command to execute. For shell commands: any valid sh command. For pseudo-commands (scan, gogo, tmux, etc.): pass them directly here."`
}

func (t *BashTool) Definition() ToolDefinition {
	return ToolDef("bash", t.Description(), BashArgs{})
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (ToolResult, error) {
	args, err := ParseArgs[BashArgs](arguments)
	if err != nil {
		return ToolResult{}, err
	}

	command := strings.TrimSpace(args.Command)
	if command == "" {
		return ToolResult{}, fmt.Errorf("empty command")
	}
	if isOnlyCommentsOrBlank(command) {
		return TextResult("ok"), nil
	}

	info, err := t.start(ctx, command, BashExecOptions{})
	if err != nil {
		return ToolResult{}, err
	}

	return t.waitOrBackground(info.ID, ctx), nil
}

// RunForeground executes command through the same tmux/registered-command
// router used by the bash agent tool, streams raw output, and waits for the
// final session state. Non-zero exits are represented by Info.ExitCode rather
// than returned as transport errors.
func (t *BashTool) RunForeground(ctx context.Context, command string, options BashExecOptions) (tmux.Info, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return tmux.Info{}, fmt.Errorf("empty command")
	}
	if isOnlyCommentsOrBlank(command) {
		if options.OnOutput != nil {
			options.OnOutput([]byte("ok"))
		}
		return tmux.Info{State: tmux.StateCompleted}, nil
	}

	info, err := t.start(ctx, command, options)
	if err != nil {
		return tmux.Info{}, err
	}

	offset := int64(0)
	flush := func() error {
		for {
			data, next, readErr := t.tasks.ReadBytesFrom(info.ID, offset, 0)
			if readErr != nil {
				return readErr
			}
			offset = next
			if len(data) > 0 && options.OnOutput != nil {
				options.OnOutput(data)
			}
			if len(data) == 0 {
				return nil
			}
		}
	}

	ticker := time.NewTicker(streamInterval)
	defer ticker.Stop()
	done := t.tasks.Done(info.ID)
	for {
		select {
		case <-done:
			if err := flush(); err != nil {
				return tmux.Info{}, err
			}
			final, _ := t.tasks.Get(info.ID)
			return final, nil
		case <-ctx.Done():
			_ = t.tasks.Kill(info.ID)
			<-done
			if err := flush(); err != nil {
				return tmux.Info{}, err
			}
			final, _ := t.tasks.Get(info.ID)
			return final, nil
		case <-ticker.C:
			if err := flush(); err != nil {
				return tmux.Info{}, err
			}
		}
	}
}

func (t *BashTool) start(ctx context.Context, command string, options BashExecOptions) (tmux.Info, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = time.Duration(t.timeout) * time.Second
	}
	workDir := options.WorkDir
	if workDir == "" {
		workDir = t.workDir
	}
	return t.tasks.RunCommand(command, tmux.RunOpts{
		Timeout: timeout,
		WorkDir: workDir,
		Env:     t.runEnv(options.Env),
		Ctx:     ctx,
	})
}

func (t *BashTool) waitOrBackground(id string, ctx context.Context) ToolResult {
	done := t.tasks.Done(id)
	select {
	case <-done:
		return t.collectResult(id)
	case <-time.After(autoBackgroundThreshold):
		info, _ := t.tasks.Get(id)
		t.startMonitor(info)
		return TextResult(fmt.Sprintf(
			"Command auto-backgrounded (exceeded %s).\nsession id=%s name=%s\nIncremental output will be delivered automatically. Use `tmux kill -t %s` to stop.",
			autoBackgroundThreshold, info.ID, info.Name, info.ID))
	case <-ctx.Done():
		_ = t.tasks.Kill(id)
		<-done
		return t.collectResult(id)
	}
}

func (t *BashTool) collectResult(id string) ToolResult {
	raw := t.tasks.PeekOrEmpty(id, truncate.DefaultMaxLines)
	r := truncate.Tail(raw, truncate.Options{})
	text := r.Content
	if r.Truncated {
		startLine := r.TotalLines - r.OutputLines + 1
		text += fmt.Sprintf(
			"\n\n[truncated: showing lines %d-%d of %d (%s of %s). Use tmux read to access earlier output.]",
			startLine, r.TotalLines, r.TotalLines, truncate.FormatSize(r.OutputBytes), truncate.FormatSize(r.TotalBytes))
	}
	info, _ := t.tasks.Get(id)
	if info.KillCause != "" {
		text += fmt.Sprintf("\n[command stopped: %s]", info.KillCause)
	}
	if info.ExitCode != 0 && info.State != tmux.StateRunning {
		text += fmt.Sprintf("\n[exit code: %d]", info.ExitCode)
	}
	return TextResult(text)
}

func (t *BashTool) runEnv(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, item := range t.proxyEnv() {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func (t *BashTool) proxyEnv() []string {
	if t.scannerProxy == "" {
		return nil
	}
	return []string{
		"ALL_PROXY=" + t.scannerProxy, "all_proxy=" + t.scannerProxy,
		"HTTP_PROXY=" + t.scannerProxy, "http_proxy=" + t.scannerProxy,
		"HTTPS_PROXY=" + t.scannerProxy, "https_proxy=" + t.scannerProxy,
	}
}

func (t *BashTool) startMonitor(info tmux.Info) {
	if t.inbox == nil {
		return
	}
	t.tasks.Monitor(info.ID, monitorInterval, func(output string) {
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			fmt.Sprintf("<session_output id=%q name=%q>\n%s\n</session_output>", info.ID, info.Name, output))
		msg.Priority = inbox.PriorityLow
		msg.Meta = map[string]any{"session_id": info.ID, "session_name": info.Name, "type": "incremental"}
		_ = t.inbox.Push(msg)
	})
}

func isOnlyCommentsOrBlank(cmdLine string) bool {
	for _, line := range strings.Split(cmdLine, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}
