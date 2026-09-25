package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	interactive bool
	route       coretool.Egress
	Name        string
	WorkDir     string
	Env         map[string]string
	Timeout     time.Duration
	TimeoutSet  bool
	OnOutput    func([]byte)
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type BashTool struct {
	baseEnv        map[string]string
	hooks          *hooks.Registry
	processMu      sync.Mutex
	processClosed  bool
	processWG      sync.WaitGroup
	workDir        string
	timeout        int
	egressProxy    string
	egressProxyCA  string
	egressResolver func(context.Context) (proxyURL, caPath string, release func())
	tasks          *procbus.Manager
	registry       coretool.CommandExecutor
	hiddenCommands map[string]struct{}
	maxTimeout     time.Duration
	closeOnce      sync.Once
	monitored      sync.Map // tmux session ID -> notification already installed
}

func NewBashTool(workDir string, timeout int, registry *hooks.Registry) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &BashTool{workDir: workDir, timeout: timeout, hooks: registry, tasks: procbus.NewManager()}
}

func (t *BashTool) Manager() *procbus.Manager      { return t.tasks }
func (t *BashTool) SetEgressProxy(proxy string)    { t.egressProxy = proxy }
func (t *BashTool) SetEgressProxyCA(caPath string) { t.egressProxyCA = caPath }
func (t *BashTool) SetEgressResolver(fn func(context.Context) (string, string, func())) {
	t.egressResolver = fn
}

// SetCommandRegistry supplies the profile-owned command boundary before use.
func (t *BashTool) SetCommandRegistry(registry coretool.CommandExecutor) {
	t.registry = registry
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
		t.tasks.Shutdown()
		t.processWG.Wait()
	})
}

// HideCommands removes control-only commands from Bash discovery.
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

func (t *BashTool) WithEgressProxy(proxy string) *BashTool {
	t.egressProxy = proxy
	return t
}

func (t *BashTool) WithEgressProxyCA(caPath string) *BashTool {
	t.egressProxyCA = caPath
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

	command := args.Command
	if strings.TrimSpace(command) == "" {
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
	if strings.TrimSpace(command) == "" {
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

// start creates one managed interpreter session for a shell script.
func (t *BashTool) start(ctx context.Context, command string, options BashExecOptions) (*coretool.Execution, error) {
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
	script, err := parseShellCommand(command)
	if err != nil {
		return nil, err
	}
	if len(script.Stmts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	env := t.runEnv(options.Env)
	if options.interactive {
		if argv, ok := t.literalExternal(script); ok {
			execution := coretool.NewExecution(t.tasks, command, argv[1:], workDir, env)
			info, err := t.tasks.Start(ctx, procSpec(options.Name, command, timeout), proc.TTY(proc.ProcOptions{
				Binary: argv[0], Args: argv[1:], Dir: workDir, Env: env,
			}))
			if err != nil {
				return nil, err
			}
			execution.BindID(info.ID)
			return execution, nil
		}
	}
	execution := coretool.NewExecution(t.tasks, command, nil, workDir, env)
	if spec, args, ok := t.literalBuiltin(script); ok {
		execution.Command, execution.Args = spec.Name, args
	}
	info, err := t.tasks.Start(ctx, procSpec(options.Name, command, timeout), t.interpreterAttachment(script, execution, options))
	if err != nil {
		return nil, err
	}
	if execution.Command != command {
		t.tasks.SetKind(info.ID, builtinSessionKind)
	}
	execution.BindID(info.ID)
	return execution, nil
}

// procSpec is the registry-level half of a bash invocation: identity and
// deadline. What the unit actually is -- a terminal, a pipe, a function -- is
// the attachment's business.
func procSpec(name, command string, timeout time.Duration) proc.Spec {
	return proc.Spec{Name: name, Command: command, Timeout: timeout, StripANSI: true}
}

func (t *BashTool) resolve(name string) (*types.CommandSpec, bool) {
	if t.registry == nil || name == "" {
		return nil, false
	}
	return t.registry.Get(name)
}

// Tagging direct registered commands keeps tmux ls focused on terminal sessions.
const builtinSessionKind = "builtin"

func joinedWriter(session, extra io.Writer) io.Writer {
	if extra == nil || extra == session {
		return session
	}
	return io.MultiWriter(session, extra)
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

func (t *BashTool) runEnv(overrides map[string]string) []string {
	values := make(map[string]string)
	for key, value := range t.baseEnv {
		values[key] = value
	}
	for _, item := range coretool.EgressEnvironment(t.egressProxy, t.egressProxyCA) {
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

// WithEnvironment sets the composition's child-process environment. Call only
// during construction; per-invocation overrides remain owned by the caller.
func (t *BashTool) WithEnvironment(values map[string]string) *BashTool {
	t.baseEnv = make(map[string]string, len(values))
	for key, value := range values {
		t.baseEnv[key] = value
	}
	return t
}
