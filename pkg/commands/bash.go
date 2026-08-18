package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/agent/tmux"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
)

const (
	defaultTimeout          = 600
	unlimitedTimeout        = time.Duration(1<<63 - 1)
	streamInterval          = 100 * time.Millisecond
	monitorInterval         = 10 * time.Second
	completionRetryInterval = 10 * time.Millisecond
)

// BashExecOptions controls one foreground execution without mutating the
// BashTool defaults. Runner/WebAgent transports use this entry point while the
// agent-facing Execute method applies the explicit wait/background contract.
type BashExecOptions struct {
	Name       string
	WorkDir    string
	Env        map[string]string
	Timeout    time.Duration
	TimeoutSet bool
	OnOutput   func([]byte)
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type BashTool struct {
	workDir        string
	timeout        int
	scannerProxy   string
	scannerProxyCA string
	egressResolver func(callID string) (proxyURL, caPath string)
	tasks          *tmux.Manager
	commandNames   func() []string
	resolveCommand func(string) (Command, bool)
	shellRegistry  *CommandRegistry
	adapterMu      sync.Mutex
	shellAdapter   *shellCommandAdapter
	closeOnce      sync.Once
}

func NewBashTool(workDir string, timeout int) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &BashTool{workDir: workDir, timeout: timeout, tasks: tmux.NewManager()}
}

func (t *BashTool) Manager() *tmux.Manager          { return t.tasks }
func (t *BashTool) SetScannerProxy(proxy string)    { t.scannerProxy = proxy }
func (t *BashTool) SetScannerProxyCA(caPath string) { t.scannerProxyCA = caPath }
func (t *BashTool) SetEgressResolver(fn func(callID string) (string, string)) {
	t.egressResolver = fn
}
func (t *BashTool) SetCommandNames(fn func() []string) { t.commandNames = fn }
func (t *BashTool) SetCommandResolver(fn func(string) (Command, bool)) {
	t.resolveCommand = fn
}
func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Close() {
	t.closeOnce.Do(func() {
		t.adapterMu.Lock()
		adapter := t.shellAdapter
		if adapter != nil {
			adapter.shutdown()
		}
		t.adapterMu.Unlock()
		t.tasks.Shutdown()
		if adapter != nil {
			adapter.cleanup()
		}
	})
}

func (t *BashTool) attachShellCommands(registry *CommandRegistry) {
	t.shellRegistry = registry
}

func (t *BashTool) ensureShellCommands() (*shellCommandAdapter, error) {
	if t.shellRegistry == nil {
		return nil, nil
	}
	t.adapterMu.Lock()
	defer t.adapterMu.Unlock()
	if t.shellAdapter == nil {
		adapter, err := newShellCommandAdapter(t.shellRegistry)
		if err != nil {
			return nil, err
		}
		t.shellAdapter = adapter
	}
	if t.commandNames != nil {
		if err := t.shellAdapter.syncAliases(t.commandNames()); err != nil {
			t.shellAdapter.close()
			t.shellAdapter = nil
			return nil, err
		}
	}
	return t.shellAdapter, nil
}

func (t *BashTool) WithScannerProxy(proxy string) *BashTool {
	t.scannerProxy = proxy
	return t
}

func (t *BashTool) WithScannerProxyCA(caPath string) *BashTool {
	t.scannerProxyCA = caPath
	return t
}

func (t *BashTool) WithEgressResolver(fn func(callID string) (string, string)) *BashTool {
	t.egressResolver = fn
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
	Wait    int    `json:"wait,omitempty" jsonschema:"minimum=0,description=Foreground wait in seconds. 0 waits until completion. A positive value moves a still-running command to background after that many seconds without canceling it."`
	Timeout int    `json:"timeout,omitempty" jsonschema:"minimum=0,description=Maximum total command runtime in seconds. 0 means unlimited when explicitly provided. Omit to use the default (600s). The timeout continues to apply after a command moves to background."`

	timeoutSet bool
}

// UnmarshalJSON preserves the distinction between an omitted timeout (use the
// tool default) and an explicit timeout of zero (no command deadline).
func (a *BashArgs) UnmarshalJSON(data []byte) error {
	var raw struct {
		Command string `json:"command"`
		Wait    int    `json:"wait"`
		Timeout *int   `json:"timeout"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.Command = raw.Command
	a.Wait = raw.Wait
	a.Timeout = 0
	a.timeoutSet = raw.Timeout != nil
	if raw.Timeout != nil {
		a.Timeout = *raw.Timeout
	}
	return nil
}

func (a BashArgs) TimeoutSpecified() bool {
	return a.timeoutSet || a.Timeout != 0
}

func (a BashArgs) Validate() error {
	if a.Wait < 0 {
		return fmt.Errorf("wait must be greater than or equal to 0")
	}
	if a.Timeout < 0 {
		return fmt.Errorf("timeout must be greater than or equal to 0")
	}
	return nil
}

func (t *BashTool) Definition() *coretool.Definition {
	return coretool.Def("bash", t.Description(), BashArgs{})
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	args, err := coretool.ParseArgs[BashArgs](arguments)
	if err != nil {
		return nil, err
	}
	if err := args.Validate(); err != nil {
		return nil, err
	}

	command := strings.TrimSpace(args.Command)
	if command == "" {
		return nil, fmt.Errorf("empty command")
	}
	if isOnlyCommentsOrBlank(command) {
		return coretool.TextResult("ok"), nil
	}

	options := BashExecOptions{WorkDir: coretool.WorkDirFromContext(ctx, "")}
	if args.TimeoutSpecified() {
		options.Timeout = time.Duration(args.Timeout) * time.Second
		options.TimeoutSet = true
	}
	execution, err := t.Start(ctx, command, options)
	if err != nil {
		return nil, err
	}

	return t.waitOrBackground(execution, ctx, inbox.FromContext(ctx), time.Duration(args.Wait)*time.Second), nil
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
// collected ToolResult (bounded text and media), streaming raw
// output through options.OnOutput. Transports that must remain foreground
// (AOP tool.call) use this instead of Execute.
func (t *BashTool) RunForegroundTool(ctx context.Context, command string, options BashExecOptions) (*coretool.Result, error) {
	execution, err := t.RunForeground(ctx, command, options)
	if err != nil {
		return nil, err
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
	if timeout < 0 {
		return nil, fmt.Errorf("timeout must be greater than or equal to 0")
	}
	if timeout == 0 && !options.TimeoutSet {
		timeout = time.Duration(t.timeout) * time.Second
	}
	if timeout == 0 {
		timeout = unlimitedTimeout
	}
	workDir := options.WorkDir
	if workDir == "" {
		workDir = t.workDir
	}
	left, right, hasPipe := splitPipeline(command)
	leftToken := firstCommandToken(left)
	if !hasPipe {
		if cmd, ok := t.resolve(leftToken); ok {
			if tokens, err := SplitCommandLine(left); err == nil {
				if args, syntaxErr := stripShellSyntax(tokens[1:]); syntaxErr == nil {
					args = normalizeNoColor(cmd.Name, args)
					return t.startBuiltin(ctx, cmd, args, timeout, workDir, t.runEnv(ctx, options.Env, nil, ""), options)
				}
			}
		}
	}
	adapter, err := t.ensureShellCommands()
	if err != nil {
		return nil, err
	}
	if adapter != nil {
		contextID := adapter.retainContext(ctx)
		env := t.runEnv(ctx, options.Env, adapter, contextID)
		execution := newExecution(t.tasks, command, nil, workDir, env)
		info, err := t.tasks.Create(workDir, command, options.Name, timeout, env, "")
		if err != nil {
			adapter.releaseContext(contextID)
			return nil, err
		}
		execution.bind(info)
		go func() {
			<-t.tasks.Done(execution.ID)
			adapter.releaseContext(contextID)
		}()
		return execution, nil
	}
	env := t.runEnv(ctx, options.Env, nil, "")
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

func (t *BashTool) waitOrBackground(execution *Execution, ctx context.Context, targetInbox inbox.Inbox, wait time.Duration) *coretool.Result {
	done := t.tasks.Done(execution.ID)
	var waitTimer *time.Timer
	var waitDone <-chan time.Time
	if wait > 0 {
		waitTimer = time.NewTimer(wait)
		waitDone = waitTimer.C
		defer waitTimer.Stop()
	}
	select {
	case <-done:
		execution.refresh()
		return t.collectResult(execution)
	case <-waitDone:
		info, ok := t.tasks.Get(execution.ID)
		if !ok {
			execution.refresh()
			return t.collectResult(execution)
		}
		t.startMonitor(info, targetInbox)
		return coretool.TextResult(fmt.Sprintf(
			"Command moved to background after waiting %s. It is still running.\nsession id=%s name=%s\nCompletion will be delivered automatically. Use `tmux kill -t %s` to stop.",
			wait, info.ID, info.Name, info.ID))
	case <-ctx.Done():
		_ = execution.Kill()
		<-done
		execution.refresh()
		return t.collectResult(execution)
	}
}

func (t *BashTool) collectResult(execution *Execution) *coretool.Result {
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
	return result
}

func (t *BashTool) runEnv(ctx context.Context, overrides map[string]string, adapter *shellCommandAdapter, shellContextID string) []string {
	values := make(map[string]string)
	for _, item := range t.proxyEnv(ctx) {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	if adapter != nil && shellContextID != "" {
		for _, item := range adapter.environment(shellContextID) {
			if key, value, ok := strings.Cut(item, "="); ok {
				values[key] = value
			}
		}
		path := values["PATH"]
		if path == "" {
			path = os.Getenv("PATH")
		}
		values["PATH"] = adapter.runtimeDir + string(os.PathListSeparator) + path
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

func (t *BashTool) proxyEnv(ctx context.Context) []string {
	proxy, ca := t.scannerProxy, t.scannerProxyCA
	// When an egress resolver is wired, it supersedes the static values: it tags
	// the proxy URL with this execution's tool-call id (so the hub attributes
	// captured flows to it) and returns the CA path from live hub state — empty
	// while the hub is not intercepting, so a relaying child is not handed a
	// CA-only bundle that would reject the real server certificate.
	if t.egressResolver != nil {
		callID := coretool.InvocationFromContext(ctx).CallID
		proxy, ca = t.egressResolver(callID)
	}
	if proxy == "" {
		return nil
	}
	env := []string{
		"ALL_PROXY=" + proxy, "all_proxy=" + proxy,
		"HTTP_PROXY=" + proxy, "http_proxy=" + proxy,
		"HTTPS_PROXY=" + proxy, "https_proxy=" + proxy,
	}
	// Point common HTTP clients at the MITM hub CA so intercepted HTTPS is
	// trusted. Tools that use the system pool or pin certs ignore these and
	// degrade to CONNECT-metadata capture, which is acceptable.
	if ca != "" {
		env = append(env,
			"CURL_CA_BUNDLE="+ca,
			"SSL_CERT_FILE="+ca,
			"NODE_EXTRA_CA_CERTS="+ca,
			"REQUESTS_CA_BUNDLE="+ca,
			"GIT_SSL_CAINFO="+ca,
		)
	}
	return env
}

func (t *BashTool) startMonitor(info tmux.Info, targetInbox inbox.Inbox) {
	if targetInbox == nil {
		return
	}
	producer := targetInbox.RegisterProducer("bash:" + info.ID)
	t.tasks.Monitor(info.ID, monitorInterval, func(output string) {
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			fmt.Sprintf("<session_output id=%q name=%q>\n%s\n</session_output>", info.ID, info.Name, output))
		msg.Priority = inbox.PriorityLow
		msg.Meta = map[string]any{"session_id": info.ID, "session_name": info.Name, "type": "incremental"}
		// Incremental output is best-effort. Completion below is high priority
		// and retried so a full inbox cannot make a background task disappear.
		_ = targetInbox.Push(msg)
	})
	go func() {
		if producer != nil {
			defer producer.Done()
		}
		<-t.tasks.Done(info.ID)
		final, ok := t.tasks.Get(info.ID)
		if !ok {
			return
		}
		tail := t.tasks.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user", tmux.FormatCompletion(final, tail))
		msg.Priority = inbox.PriorityHigh
		msg.Meta = map[string]any{
			"session_id":   final.ID,
			"session_name": final.Name,
			"exit_code":    final.ExitCode,
			"type":         "completion",
		}
		pushCompletion(targetInbox, msg)
	}()
}

func pushCompletion(targetInbox inbox.Inbox, msg inbox.Message) {
	for {
		err := targetInbox.Push(msg)
		switch {
		case err == nil, errors.Is(err, inbox.ErrInboxClosed):
			return
		case !errors.Is(err, inbox.ErrInboxFull):
			return
		}
		if targetInbox.Closed() {
			return
		}
		time.Sleep(completionRetryInterval)
	}
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
