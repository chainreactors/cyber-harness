package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	procbus "github.com/chainreactors/cyber/core/proc"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/truncate"
	"github.com/chainreactors/cyber/core/types"

	"github.com/chainreactors/cyber/pkg/output"
	"github.com/chainreactors/utils/proc"
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
	foreground bool
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

// ProcessContainment is a process resource supplied by profiles that require
// a stronger descendant boundary than the host shell provides. Bash invokes it
// only for paths that actually start a shell. The profile that constructs the
// resource remains responsible for closing it.
type ProcessContainment interface {
	Prepare(string, BashExecOptions) (BashExecOptions, func(), error)
}

type BashTool struct {
	baseEnv            map[string]string
	hooks              *hooks.Registry
	processMu          sync.Mutex
	processClosed      bool
	processWG          sync.WaitGroup
	workDir            string
	timeout            int
	scannerProxy       string
	scannerProxyCA     string
	egressResolver     func(context.Context) (proxyURL, caPath string, release func())
	tasks              *procbus.Manager
	registry           coretool.CommandExecutor
	shellCommands      bool
	hiddenCommands     map[string]struct{}
	standaloneCommands map[string]struct{}
	adapterMu          sync.Mutex
	shellAdapter       *shellCommandAdapter
	containment        ProcessContainment
	maxTimeout         time.Duration
	closeOnce          sync.Once
	monitored          sync.Map // tmux session ID -> notification already installed
}

func NewBashTool(workDir string, timeout int, registry *hooks.Registry) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &BashTool{workDir: workDir, timeout: timeout, hooks: registry, tasks: procbus.NewManager()}
}

func (t *BashTool) Manager() *procbus.Manager       { return t.tasks }
func (t *BashTool) SetScannerProxy(proxy string)    { t.scannerProxy = proxy }
func (t *BashTool) SetScannerProxyCA(caPath string) { t.scannerProxyCA = caPath }
func (t *BashTool) SetEgressResolver(fn func(context.Context) (string, string, func())) {
	t.egressResolver = fn
}

// SetCommandRegistry supplies the profile-owned command boundary before use.
func (t *BashTool) SetCommandRegistry(registry coretool.CommandExecutor) {
	t.registry = registry
}

func (t *BashTool) WithProcessContainment(containment ProcessContainment) *BashTool {
	t.containment = containment
	return t
}

func (t *BashTool) WithForegroundTimeoutCeiling(max time.Duration) *BashTool {
	t.maxTimeout = max
	return t
}

func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Close() {
	t.closeOnce.Do(func() {
		t.processMu.Lock()
		t.processClosed = true
		t.processMu.Unlock()
		t.adapterMu.Lock()
		adapter := t.shellAdapter
		if adapter != nil {
			adapter.shutdown()
		}
		t.adapterMu.Unlock()
		t.tasks.Shutdown()
		t.processWG.Wait()
		if adapter != nil {
			adapter.cleanup()
		}
	})
}

func (t *BashTool) attachShellCommands(registry coretool.CommandExecutor) {
	t.registry = registry
	t.shellCommands = true
}

// EnableShellCommands binds the pseudo-command registry used when a shell line
// composes registered commands. Profiles call this before publication.
func (t *BashTool) EnableShellCommands(registry coretool.CommandExecutor) {
	t.attachShellCommands(registry)
}

// StandaloneCommands keeps commands discoverable but prevents their invocation
// from shell adapters. Only direct registry dispatch may start these commands.
func (t *BashTool) StandaloneCommands(names ...string) {
	t.standaloneCommands = make(map[string]struct{}, len(names))
	for _, name := range names {
		t.standaloneCommands[name] = struct{}{}
	}
}

// HideCommands removes control-only commands from Bash discovery and shell
// aliases while leaving direct, policy-checked registry execution available.
// Profiles configure this before publishing the Bash tool.
func (t *BashTool) HideCommands(names ...string) {
	if t.hiddenCommands == nil {
		t.hiddenCommands = make(map[string]struct{}, len(names))
	}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			t.hiddenCommands[name] = struct{}{}
		}
	}
}

func (t *BashTool) commandNames() []string {
	if t.registry == nil {
		return nil
	}
	names := t.registry.Names()
	if len(t.hiddenCommands) == 0 {
		return names
	}
	visible := names[:0]
	for _, name := range names {
		if _, hidden := t.hiddenCommands[name]; !hidden {
			visible = append(visible, name)
		}
	}
	return visible
}

func (t *BashTool) ensureShellCommands() (*shellCommandAdapter, error) {
	if !t.shellCommands || t.registry == nil {
		return nil, nil
	}
	t.adapterMu.Lock()
	defer t.adapterMu.Unlock()
	if t.shellAdapter == nil {
		adapter, err := newShellCommandAdapter(t.registry)
		if err != nil {
			return nil, err
		}
		t.shellAdapter = adapter
	}
	names := t.commandNames()
	allowed := names[:0]
	for _, name := range names {
		if _, standalone := t.standaloneCommands[name]; !standalone {
			allowed = append(allowed, name)
		}
	}
	if err := t.shellAdapter.syncAliases(allowed); err != nil {
		t.shellAdapter.close()
		t.shellAdapter = nil
		return nil, err
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

func (t *BashTool) WithEgressResolver(fn func(context.Context) (string, string, func())) *BashTool {
	t.egressResolver = fn
	return t
}

func (t *BashTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.registry != nil {
		if names := t.commandNames(); len(names) > 0 {
			desc += " IMPORTANT: This tool also handles pseudo-commands (" + strings.Join(names, ", ") + "). Pass them as the command parameter."
		}
	}
	return desc
}

type BashArgs struct {
	Command string `json:"command" jsonschema:"description=The command to execute. For shell commands: any valid sh command. For pseudo-commands (scan, gogo, tmux, etc.): pass them directly here."`
	Wait    int    `json:"wait,omitempty" jsonschema:"minimum=0,description=Foreground wait in seconds. 0 waits until completion unless interrupted by Inbox. A positive value moves a still-running command to background after that many seconds without canceling it. Interruption also releases the wait; the same tmux session continues."`
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
	if progress := operation.InvocationFromContext(ctx).Progress; progress != nil {
		options := BashExecOptions{WorkDir: operation.WorkDirFromContext(ctx, ""), OnOutput: progress}
		if args.TimeoutSpecified() {
			options.Timeout = time.Duration(args.Timeout) * time.Second
			options.TimeoutSet = true
		}
		return t.RunForegroundTool(ctx, command, options)
	}

	options := BashExecOptions{WorkDir: operation.WorkDirFromContext(ctx, "")}
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
// final session state. Non-zero exits are represented by Info.ExitStatus() rather
// than returned as transport errors.
func (t *BashTool) RunForeground(ctx context.Context, command string, options BashExecOptions) (*coretool.Execution, error) {
	options.foreground = true
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("empty command")
	}
	if options.WorkDir == "" {
		options.WorkDir = operation.WorkDirFromContext(ctx, "")
	}
	if isOnlyCommentsOrBlank(command) {
		if options.OnOutput != nil {
			options.OnOutput([]byte("ok"))
		}
		return &coretool.Execution{Command: command}, nil
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
			if err := execution.WaitProcessCompletion(context.WithoutCancel(ctx)); err != nil {
				return nil, err
			}
			return execution, nil
		case <-ctx.Done():
			_ = execution.Kill()
			<-done
			if err := flush(); err != nil {
				return nil, err
			}
			if err := execution.WaitProcessCompletion(context.WithoutCancel(ctx)); err != nil {
				return nil, err
			}
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
	if t.maxTimeout > 0 && options.Timeout > t.maxTimeout {
		return nil, fmt.Errorf("foreground bash timeout %s exceeds runner ceiling %s", options.Timeout, t.maxTimeout)
	}
	execution, err := t.RunForeground(ctx, command, options)
	if err != nil {
		return nil, err
	}
	result := t.collectResult(execution)
	return result, nil
}

// Start resolves command through the built-in registry or the system shell and
// always returns an Execution backed by one PTY session.
func (t *BashTool) start(ctx context.Context, command string, options BashExecOptions) (*coretool.Execution, error) {
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
	// Inspect the original line before tokenization loses quotes and operator
	// boundaries (for example "port;", "--help&&echo", or a newline).
	if !hasPipe && shellSyntaxIndex(command) < 0 {
		if cmd, ok := t.resolve(leftToken); ok {
			if tokens, err := coretool.SplitCommandLine(left); err == nil {
				return t.startBuiltin(ctx, cmd, tokens[1:], timeout, workDir, t.runEnv(ctx, options.Env, nil, ""), options)
			}
		}
	}
	if _, standalone := t.standaloneCommands[leftToken]; standalone {
		return nil, fmt.Errorf("%s must be a standalone command without shell composition", leftToken)
	}
	adapter, err := t.ensureShellCommands()
	if err != nil {
		return nil, err
	}
	if adapter != nil {
		var cleanup func()
		options, cleanup, err = t.prepareShell(command, options)
		if err != nil {
			return nil, err
		}
		contextID := adapter.retainContext(ctx)
		env := t.runEnv(ctx, options.Env, adapter, contextID)
		execution := coretool.NewExecution(t.tasks, command, nil, workDir, env)
		info, err := t.tasks.Start(ctx, procSpec(options.Name, command, timeout),
			shellAttachment(command, workDir, env, options))
		if err != nil {
			adapter.releaseContext(contextID)
			cleanup()
			return nil, err
		}
		execution.BindID(info.ID)
		go func() {
			<-t.tasks.Done(execution.ID)
			adapter.releaseContext(contextID)
		}()
		t.releaseProcess(cleanup, execution)
		return execution, nil
	}
	env := t.runEnv(ctx, options.Env, nil, "")
	if cmd, ok := t.resolve(leftToken); ok {
		tokens, err := coretool.SplitCommandLine(left)
		if err != nil {
			return nil, err
		}
		args, err := coretool.StripShellSyntax(tokens[1:])
		if err != nil {
			return nil, err
		}
		if !hasPipe && shellSyntaxIndex(command) >= 0 {
			return nil, fmt.Errorf("shell composition for %s requires the shell command adapter; issue the command separately", leftToken)
		}
		if hasPipe && right != "" {
			options, cleanup, err := t.prepareShell(command, options)
			if err != nil {
				return nil, err
			}
			env = t.runEnv(ctx, options.Env, nil, "")
			execution, err := t.startBuiltinToShell(ctx, cmd, args, right, timeout, workDir, env, options)
			if err != nil {
				cleanup()
				return nil, err
			}
			t.releaseProcess(cleanup, execution)
			return execution, nil
		}
		return t.startBuiltin(ctx, cmd, args, timeout, workDir, env, options)
	}
	if hasPipe && right != "" {
		rightToken := firstCommandToken(right)
		if cmd, ok := t.resolve(rightToken); ok {
			tokens, err := coretool.SplitCommandLine(right)
			if err != nil {
				return nil, err
			}
			args, err := coretool.StripShellSyntax(tokens[1:])
			if err != nil {
				return nil, err
			}
			options, cleanup, err := t.prepareShell(command, options)
			if err != nil {
				return nil, err
			}
			env = t.runEnv(ctx, options.Env, nil, "")
			execution, err := t.startShellToBuiltin(ctx, left, cmd, args, timeout, workDir, env, options)
			if err != nil {
				cleanup()
				return nil, err
			}
			t.releaseProcess(cleanup, execution)
			return execution, nil
		}
	}
	options, cleanup, err := t.prepareShell(command, options)
	if err != nil {
		return nil, err
	}
	env = t.runEnv(ctx, options.Env, nil, "")
	execution := coretool.NewExecution(t.tasks, command, nil, workDir, env)
	info, err := t.tasks.Start(ctx, procSpec(options.Name, command, timeout),
		shellAttachment(command, workDir, env, options))
	if err != nil {
		cleanup()
		return nil, err
	}
	execution.BindID(info.ID)
	t.releaseProcess(cleanup, execution)
	return execution, nil
}

func shellAttachment(command, workDir string, env []string, options BashExecOptions) proc.Attachment {
	attachment := proc.TTY(proc.ProcOptions{Line: command, Dir: workDir, Env: env})
	if !options.foreground || runtime.GOOS != "windows" {
		return attachment
	}
	return proc.AttachFunc(func(ctx context.Context) (*proc.Attached, error) {
		attached, err := attachment.Start(ctx)
		if err != nil {
			return nil, err
		}
		// Ctrl+C on a Windows terminal can interrupt only the current child,
		// allowing cmd.exe to execute the remainder of a canceled command line.
		// Foreground RPC cancellation must terminate the whole invocation. Keep
		// the registry's one stop/drain lifecycle, but skip interactive signals.
		signal := attached.Signal
		attached.Signal = func(proc.Signal) error { return signal(proc.SigKill) }
		return attached, nil
	})
}

// procSpec is the registry-level half of a bash invocation: identity and
// deadline. What the unit actually is -- a terminal, a pipe, a function -- is
// the attachment's business.
func procSpec(name, command string, timeout time.Duration) proc.Spec {
	return proc.Spec{Name: name, Command: command, Timeout: timeout, StripANSI: true}
}

func (t *BashTool) prepareShell(command string, options BashExecOptions) (BashExecOptions, func(), error) {
	if t.containment == nil {
		return options, func() {}, nil
	}
	prepared, cleanup, err := t.containment.Prepare(command, options)
	if cleanup == nil {
		cleanup = func() {}
	}
	return prepared, cleanup, err
}

func (t *BashTool) releaseProcess(cleanup func(), execution *coretool.Execution) {
	if cleanup == nil || execution == nil || execution.ID == "" {
		if cleanup != nil {
			cleanup()
		}
		return
	}
	go func(id string) {
		<-t.tasks.Done(id)
		cleanup()
	}(execution.ID)
}

func (t *BashTool) resolve(name string) (*types.CommandSpec, bool) {
	if t.registry == nil || name == "" {
		return nil, false
	}
	return t.registry.Get(name)
}

// Built-in commands run in-process and borrow the session manager for timeouts
// and output capture. Tagging them keeps `tmux ls` to terminal sessions: without
// it the listing command reports itself as a session.
const builtinSessionKind = "builtin"

func (t *BashTool) startBuiltin(
	ctx context.Context,
	command *types.CommandSpec,
	args []string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*coretool.Execution, error) {
	execution := coretool.NewExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.Start(ctx, procSpec(name, name, timeout), proc.Func(func(runCtx context.Context, session io.Writer) error {
		stdout := joinedWriter(session, options.Stdout)
		stderr := joinedWriter(session, options.Stderr)
		execution.SetIO(options.Stdin, stdout, stderr)
		details, runErr := t.registry.Execute(runCtx, command.Name, execution)
		execution.SetDetails(details)
		return runErr
	}))
	if err != nil {
		return nil, err
	}
	t.tasks.SetKind(info.ID, builtinSessionKind)
	execution.BindID(info.ID)
	return execution, nil
}

func (t *BashTool) startBuiltinToShell(
	ctx context.Context,
	command *types.CommandSpec,
	args []string,
	pipeline string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*coretool.Execution, error) {
	execution := coretool.NewExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.Start(ctx, procSpec(name, name, timeout), proc.Func(func(runCtx context.Context, session io.Writer) error {
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

		execution.SetIO(options.Stdin, writer, joinedWriter(session, options.Stderr))
		details, commandErr := t.registry.Execute(runCtx, command.Name, execution)
		execution.SetDetails(details)
		_ = writer.CloseWithError(commandErr)
		shellErr := <-shellDone
		if commandErr != nil {
			return commandErr
		}
		return shellErr
	}))
	if err != nil {
		return nil, err
	}
	t.tasks.SetKind(info.ID, builtinSessionKind)
	execution.BindID(info.ID)
	return execution, nil
}

func (t *BashTool) startShellToBuiltin(
	ctx context.Context,
	shellLine string,
	command *types.CommandSpec,
	args []string,
	timeout time.Duration,
	workDir string,
	env []string,
	options BashExecOptions,
) (*coretool.Execution, error) {
	execution := coretool.NewExecution(t.tasks, command.Name, args, workDir, env)
	name := options.Name
	if name == "" {
		name = command.Name
	}
	info, err := t.tasks.Start(ctx, procSpec(name, name, timeout), proc.Func(func(runCtx context.Context, session io.Writer) error {
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

		execution.SetIO(reader, joinedWriter(session, options.Stdout), joinedWriter(session, options.Stderr))
		details, commandErr := t.registry.Execute(runCtx, command.Name, execution)
		execution.SetDetails(details)
		_ = reader.Close()
		shellErr := <-shellDone
		if commandErr != nil {
			return commandErr
		}
		return shellErr
	}))
	if err != nil {
		return nil, err
	}
	t.tasks.SetKind(info.ID, builtinSessionKind)
	execution.BindID(info.ID)
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

func (t *BashTool) waitOrBackground(execution *coretool.Execution, ctx context.Context, targetInbox inbox.Inbox, wait time.Duration) *coretool.Result {
	done := t.tasks.Done(execution.ID)
	var interrupt <-chan struct{}
	if targetInbox != nil {
		interrupt = targetInbox.InterruptSignal()
	}
	// Explicit tmux waits handle their target themselves. Do not background the
	// short-lived command wrapper as well as the session it is waiting for.
	if execution.Command == "tmux" {
		interrupt = nil
	}
	var waitTimer *time.Timer
	var waitDone <-chan time.Time
	if wait > 0 {
		waitTimer = time.NewTimer(wait)
		waitDone = waitTimer.C
		defer waitTimer.Stop()
	}
	select {
	case <-done:
		return t.collectResult(execution)
	case <-waitDone:
		return t.background(execution, targetInbox, fmt.Sprintf("after waiting %s", wait))
	case <-interrupt:
		if ctx.Err() != nil {
			_ = execution.Kill()
			<-done
			return t.collectResult(execution)
		}
		return t.background(execution, targetInbox, "because an interrupting Inbox message arrived")
	case <-ctx.Done():
		_ = execution.Kill()
		<-done
		return t.collectResult(execution)
	}
}

func (t *BashTool) background(execution *coretool.Execution, targetInbox inbox.Inbox, reason string) *coretool.Result {
	info, ok := t.tasks.Get(execution.ID)
	if !ok || info.State != proc.StateRunning {
		return t.collectResult(execution)
	}
	if !execution.DetachParent() {
		// The owner already canceled; do not claim a canceled process survives.
		<-t.tasks.Done(execution.ID)
		return t.collectResult(execution)
	}
	t.startMonitor(info, targetInbox)
	return coretool.TextResult(fmt.Sprintf(
		"Command moved to background %s. It is still managed by tmux.\nsession id=%s name=%s\nUse `tmux capture-pane -t %s` to inspect output or `tmux kill-session -t %s` to stop. Completion is delivered to the calling Inbox when present.",
		reason, info.ID, info.Name, info.ID, info.ID))
}

func (t *BashTool) collectResult(execution *coretool.Execution) *coretool.Result {
	fullCapture := execution != nil && execution.Command == "tmux" && len(execution.Args) >= 2 &&
		(execution.Args[0] == "capture-pane" || execution.Args[0] == "peek") && contains(execution.Args[1:], "--full")
	raw := t.tasks.PeekOrEmpty(execution.ID, truncate.DefaultMaxLines)
	truncateOptions := truncate.Options{}
	if fullCapture {
		const markerHeadroom = 8 * 1024
		if data, _, err := t.tasks.SnapshotBytes(execution.ID, 512*1024+markerHeadroom); err == nil {
			raw = string(data)
		}
		truncateOptions = truncate.Options{MaxLines: 1 << 30, MaxBytes: 512*1024 + markerHeadroom}
	}
	r := truncate.Tail(output.StripANSI(raw), truncateOptions)
	text := r.Content
	if r.Truncated {
		startLine := r.TotalLines - r.OutputLines + 1
		text += fmt.Sprintf(
			"\n\n[truncated: showing lines %d-%d of %d (%s of %s). Use tmux read to access earlier output.]",
			startLine, r.TotalLines, r.TotalLines, truncate.FormatSize(r.OutputBytes), truncate.FormatSize(r.TotalBytes))
	}
	info, _ := t.tasks.Get(execution.ID)
	if info.Reason != "" {
		text += fmt.Sprintf("\n[command stopped: %s]", info.Reason)
	}
	if info.ExitStatus() != 0 && info.State != proc.StateRunning {
		text += fmt.Sprintf("\n[exit code: %d]", info.ExitStatus())
	}
	result := coretool.TextResult(text)
	result.IsError = info.Reason != "" || (info.ExitStatus() != 0 && info.State != proc.StateRunning)
	return result
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (t *BashTool) runEnv(ctx context.Context, overrides map[string]string, adapter *shellCommandAdapter, shellContextID string) []string {
	values := make(map[string]string)
	for key, value := range t.baseEnv {
		values[key] = value
	}
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
	// Point the same common proxy/CA surface at child processes that built-in
	// tools consume through Execution.Env.
	return coretool.EgressEnvironment(proxy, ca)
}

func (t *BashTool) startMonitor(info proc.Info, targetInbox inbox.Inbox) {
	if targetInbox == nil {
		return
	}
	if _, loaded := t.monitored.LoadOrStore(info.ID, struct{}{}); loaded {
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
		msg := inbox.NewMessage(inbox.OriginSession, "user", FormatCompletion(final, tail))
		msg.Priority = inbox.PriorityHigh
		msg.Meta = map[string]any{
			"session_id":   final.ID,
			"session_name": final.Name,
			"exit_code":    final.ExitStatus(),
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
	tokens, err := coretool.SplitCommandLine(input)
	if err != nil || len(tokens) == 0 {
		return ""
	}
	// Shell separators need not have whitespace on either side. Only inspect
	// the command word here, so substitutions in later arguments cannot turn
	// a known command into an empty/unterminated command name.
	name := tokens[0]
	if index := strings.IndexAny(name, ";|&<>"); index >= 0 {
		name = name[:index]
	}
	return name
}

// shellSyntaxIndex locates syntax that needs shell evaluation. Quoted literal
// operators stay in argv; substitutions in double quotes still need the shell.
func shellSyntaxIndex(input string) int {
	var quote rune
	escaped := false
	for index, value := range input {
		if escaped {
			escaped = false
			continue
		}
		if value == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if value == quote {
				quote = 0
				continue
			}
			if quote == '"' && (value == '$' || value == '`') {
				return index
			}
			continue
		}
		if value == '\'' || value == '"' {
			quote = value
			continue
		}
		if strings.ContainsRune(";|&<>\n\r$`()", value) {
			return index
		}
	}
	return -1
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

// WithEnvironment sets the composition's child-process environment. Call only
// during construction; per-invocation overrides remain owned by the caller.
func (t *BashTool) WithEnvironment(values map[string]string) *BashTool {
	t.baseEnv = make(map[string]string, len(values))
	for key, value := range values {
		t.baseEnv[key] = value
	}
	return t
}
