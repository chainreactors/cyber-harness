package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	coretool "github.com/chainreactors/aiscan/core/tool"
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
	Name     string
	WorkDir  string
	Env      map[string]string
	Timeout  time.Duration
	OnOutput func([]byte)
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

type BashTool struct {
	workDir        string
	timeout        int
	scannerProxy   string
	tasks          *tmux.Manager
	commandNames   func() []string
	resolveCommand func(string) (Command, bool)
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
	t.resolveCommand = fn
}
func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Close()       { t.tasks.Shutdown() }

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
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=Optional timeout in seconds. The command is killed when it exceeds this. Omit to use the default (300s). Commands still running after 15s are moved to background and keep running until this timeout."`
}

func (t *BashTool) Definition() coretool.Definition {
	return coretool.Def("bash", t.Description(), BashArgs{})
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (coretool.Result, error) {
	args, err := coretool.ParseArgs[BashArgs](arguments)
	if err != nil {
		return coretool.Result{}, err
	}

	command := strings.TrimSpace(args.Command)
	if command == "" {
		return coretool.Result{}, fmt.Errorf("empty command")
	}
	if isOnlyCommentsOrBlank(command) {
		return coretool.TextResult("ok"), nil
	}

	options := BashExecOptions{}
	options.WorkDir = coretool.WorkDirFromContext(ctx, "")
	if args.Timeout > 0 {
		options.Timeout = time.Duration(args.Timeout) * time.Second
	}
	execution, err := t.Start(ctx, command, options)
	if err != nil {
		return coretool.Result{}, err
	}

	return t.waitOrBackground(execution, ctx, inboxFromContext(ctx)), nil
}

// RunForeground executes command through the same tmux/registered-command
// router used by the bash agent tool, streams raw output, and waits for the
// final session state. Non-zero exits are represented by Info.ExitCode rather
// than returned as transport errors.
func (t *BashTool) RunForeground(ctx context.Context, command string, options BashExecOptions) (*Execution, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("empty command")
	}
	if isOnlyCommentsOrBlank(command) {
		if options.OnOutput != nil {
			options.OnOutput([]byte("ok"))
		}
		return &Execution{Command: command, State: tmux.StateCompleted}, nil
	}

	execution, err := t.Start(ctx, command, options)
	if err != nil {
		return nil, err
	}

	offset := int64(0)
	flush := func() error {
		for {
			data, next, readErr := t.tasks.ReadBytesFrom(execution.ID, offset, 0)
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
	done := t.tasks.Done(execution.ID)
	for {
		select {
		case <-done:
			if err := flush(); err != nil {
				return nil, err
			}
			execution.refresh()
			return execution, nil
		case <-ctx.Done():
			_ = execution.Kill()
			<-done
			if err := flush(); err != nil {
				return nil, err
			}
			execution.refresh()
			return execution, nil
		case <-ticker.C:
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
}

// RunForegroundTool executes a command in the foreground and returns the
// collected ToolResult (bounded text plus structured Details), streaming raw
// output through options.OnOutput. Transports that must not auto-background
// (AOP tool.call) use this instead of Execute.
func (t *BashTool) RunForegroundTool(ctx context.Context, command string, options BashExecOptions) (coretool.Result, error) {
	execution, err := t.RunForeground(ctx, command, options)
	if err != nil {
		return coretool.Result{}, err
	}
	return t.collectResult(execution), nil
}

// Start resolves command through the built-in registry or the system shell and
// always returns an Execution backed by one PTY session.
func (t *BashTool) Start(ctx context.Context, command string, options BashExecOptions) (*Execution, error) {
	command = stripCommentsAndBlanks(command)
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("empty command")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = time.Duration(t.timeout) * time.Second
	}
	workDir := options.WorkDir
	if workDir == "" {
		workDir = t.workDir
	}
	env := t.runEnv(options.Env)
	left, right, hasPipe := splitPipeline(command)
	leftToken := firstCommandToken(left)
	if cmd, ok := t.resolve(leftToken); ok {
		tokens, err := SplitCommandLine(left)
		if err != nil {
			return nil, err
		}
		args, err := stripShellSyntax(tokens[1:])
		if err != nil {
			return nil, err
		}
		args = normalizeNoColor(cmd.Name, args)
		if hasPipe && right != "" {
			return t.startBuiltinToShell(ctx, cmd, args, right, timeout, workDir, env, options)
		}
		return t.startBuiltin(ctx, cmd, args, timeout, workDir, env, options)
	}
	if hasPipe && right != "" {
		rightToken := firstCommandToken(right)
		if cmd, ok := t.resolve(rightToken); ok {
			tokens, err := SplitCommandLine(right)
			if err != nil {
				return nil, err
			}
			args, err := stripShellSyntax(tokens[1:])
			if err != nil {
				return nil, err
			}
			args = normalizeNoColor(cmd.Name, args)
			return t.startShellToBuiltin(ctx, left, cmd, args, timeout, workDir, env, options)
		}
	}
	execution := newExecution(t.tasks, command, nil, workDir, env)
	info, err := t.tasks.Create(workDir, command, options.Name, timeout, env, "")
	if err != nil {
		return nil, err
	}
	execution.bind(info)
	return execution, nil
}

func (t *BashTool) resolve(name string) (Command, bool) {
	if t.resolveCommand == nil || name == "" {
		return Command{}, false
	}
	return t.resolveCommand(name)
}

func (t *BashTool) startBuiltin(
	ctx context.Context,
	command Command,
	args []string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*Execution, error) {
	execution := newExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.CreateFunc(ctx, name, timeout, func(runCtx context.Context, session io.Writer) error {
		stdout := joinedWriter(session, options.Stdout)
		stderr := joinedWriter(session, options.Stderr)
		execution.setIO(options.Stdin, stdout, stderr)
		if command.Run == nil {
			return fmt.Errorf("command %s has no runner", command.Name)
		}
		details, runErr := command.Run(runCtx, execution)
		execution.setDetails(details)
		return runErr
	})
	if err != nil {
		return nil, err
	}
	execution.bind(info)
	return execution, nil
}

func (t *BashTool) startBuiltinToShell(
	ctx context.Context,
	command Command,
	args []string,
	pipeline string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*Execution, error) {
	execution := newExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.CreateFunc(ctx, name, timeout, func(runCtx context.Context, session io.Writer) error {
		reader, writer := io.Pipe()
		sh := exec.CommandContext(runCtx, "sh", "-c", pipeline)
		sh.Stdin = reader
		sh.Stdout = joinedWriter(session, options.Stdout)
		sh.Stderr = joinedWriter(session, options.Stderr)
		configureProcess(sh, workDir, env)
		shellDone := make(chan error, 1)
		go func() {
			shellDone <- sh.Run()
			_ = reader.Close()
		}()

		execution.setIO(options.Stdin, writer, joinedWriter(session, options.Stderr))
		if command.Run == nil {
			_ = writer.Close()
			<-shellDone
			return fmt.Errorf("command %s has no runner", command.Name)
		}
		details, commandErr := command.Run(runCtx, execution)
		execution.setDetails(details)
		_ = writer.CloseWithError(commandErr)
		shellErr := <-shellDone
		if commandErr != nil {
			return commandErr
		}
		return shellErr
	})
	if err != nil {
		return nil, err
	}
	execution.bind(info)
	return execution, nil
}

func (t *BashTool) startShellToBuiltin(
	ctx context.Context,
	shellLine string,
	command Command,
	args []string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*Execution, error) {
	execution := newExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.CreateFunc(ctx, name, timeout, func(runCtx context.Context, session io.Writer) error {
		reader, writer := io.Pipe()
		sh := exec.CommandContext(runCtx, "sh", "-c", shellLine)
		sh.Stdin = options.Stdin
		sh.Stdout = writer
		sh.Stderr = joinedWriter(session, options.Stderr)
		configureProcess(sh, workDir, env)
		shellDone := make(chan error, 1)
		go func() {
			err := sh.Run()
			_ = writer.CloseWithError(err)
			shellDone <- err
		}()

		execution.setIO(reader, joinedWriter(session, options.Stdout), joinedWriter(session, options.Stderr))
		if command.Run == nil {
			_ = reader.Close()
			<-shellDone
			return fmt.Errorf("command %s has no runner", command.Name)
		}
		details, commandErr := command.Run(runCtx, execution)
		execution.setDetails(details)
		_ = reader.Close()
		shellErr := <-shellDone
		if commandErr != nil {
			return commandErr
		}
		return shellErr
	})
	if err != nil {
		return nil, err
	}
	execution.bind(info)
	return execution, nil
}

func joinedWriter(session, extra io.Writer) io.Writer {
	if extra == nil || extra == session {
		return session
	}
	return io.MultiWriter(session, extra)
}

func configureProcess(cmd *exec.Cmd, workDir string, env []string) {
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
}

func (t *BashTool) waitOrBackground(execution *Execution, ctx context.Context, targetInbox inbox.Inbox) coretool.Result {
	done := t.tasks.Done(execution.ID)
	select {
	case <-done:
		execution.refresh()
		return t.collectResult(execution)
	case <-time.After(autoBackgroundThreshold):
		info, _ := t.tasks.Get(execution.ID)
		t.startMonitor(info, targetInbox)
		return coretool.TextResult(fmt.Sprintf(
			"Command auto-backgrounded (exceeded %s).\nsession id=%s name=%s\nIncremental output will be delivered automatically. Use `tmux kill -t %s` to stop.",
			autoBackgroundThreshold, info.ID, info.Name, info.ID))
	case <-ctx.Done():
		_ = execution.Kill()
		<-done
		execution.refresh()
		return t.collectResult(execution)
	}
}

func (t *BashTool) collectResult(execution *Execution) coretool.Result {
	raw := t.tasks.PeekOrEmpty(execution.ID, truncate.DefaultMaxLines)
	r := truncate.Tail(raw, truncate.Options{})
	text := r.Content
	if r.Truncated {
		startLine := r.TotalLines - r.OutputLines + 1
		text += fmt.Sprintf(
			"\n\n[truncated: showing lines %d-%d of %d (%s of %s). Use tmux read to access earlier output.]",
			startLine, r.TotalLines, r.TotalLines, truncate.FormatSize(r.OutputBytes), truncate.FormatSize(r.TotalBytes))
	}
	info, _ := t.tasks.Get(execution.ID)
	if info.KillCause != "" {
		text += fmt.Sprintf("\n[command stopped: %s]", info.KillCause)
	}
	if info.ExitCode != 0 && info.State != tmux.StateRunning {
		text += fmt.Sprintf("\n[exit code: %d]", info.ExitCode)
	}
	result := coretool.TextResult(text)
	result.Details = execution.Details
	return result
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

func (t *BashTool) startMonitor(info tmux.Info, targetInbox inbox.Inbox) {
	if targetInbox == nil {
		return
	}
	t.tasks.Monitor(info.ID, monitorInterval, func(output string) {
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			fmt.Sprintf("<session_output id=%q name=%q>\n%s\n</session_output>", info.ID, info.Name, output))
		msg.Priority = inbox.PriorityLow
		msg.Meta = map[string]any{"session_id": info.ID, "session_name": info.Name, "type": "incremental"}
		_ = targetInbox.Push(msg)
	})
	go func() {
		<-t.tasks.Done(info.ID)
		final, ok := t.tasks.Get(info.ID)
		if !ok {
			return
		}
		tail := t.tasks.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user", tmux.FormatCompletion(final, tail))
		msg.Meta = map[string]any{
			"session_id":   final.ID,
			"session_name": final.Name,
			"exit_code":    final.ExitCode,
		}
		_ = targetInbox.Push(msg)
	}()
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

func stripCommentsAndBlanks(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func firstCommandToken(input string) string {
	tokens, err := SplitCommandLine(input)
	if err != nil || len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

func splitPipeline(commandLine string) (left, right string, ok bool) {
	var quote rune
	escaped := false
	runes := []rune(commandLine)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '|' {
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
				continue
			}
			return strings.TrimSpace(string(runes[:i])), strings.TrimSpace(string(runes[i+1:])), true
		}
	}
	return commandLine, "", false
}
