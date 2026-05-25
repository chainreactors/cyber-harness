package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/agent/task"
)

const (
	maxOutputSize      = 50 * 1024 // 50KB
	defaultTimeout     = 300       // 5 minutes
	foregroundStopWait = 5 * time.Second
)

type BashTool struct {
	registry     *CommandRegistry
	workDir      string
	timeout      int
	scannerProxy string
	tasks        *task.Manager
}

// NewBashTool constructs a bash tool. The internal task.Manager handles
// background:true invocations; expose it via Manager() so the task tool
// can share the same instance.
func NewBashTool(workDir string, timeout int, registry *CommandRegistry) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if registry != nil && workDir != "" {
		registry.SetWorkDir(workDir)
	}
	return &BashTool{
		registry: registry,
		workDir:  workDir,
		timeout:  timeout,
		tasks:    task.NewManager(),
	}
}

// Manager exposes the task manager so a sibling task tool (and the swarm
// glue in cmd/runners.go) can set the inbox sink or kill leftover tasks.
func (t *BashTool) Manager() *task.Manager { return t.tasks }

func (t *BashTool) WithScannerProxy(proxy string) *BashTool {
	t.scannerProxy = proxy
	return t
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.registry != nil {
		names := t.registry.Names()
		if len(names) > 0 {
			desc += " IMPORTANT: This tool also handles pseudo-commands (" + strings.Join(names, ", ") + "). Pass them as the command parameter — e.g. {\"command\": \"scan -i 10.0.0.1 --mode quick\"}. These are NOT system binaries; they only work through this bash tool."
		}
	}
	return desc
}

func (t *BashTool) Close() {
	t.tasks.Shutdown()
}

type BashArgs struct {
	Command            string `json:"command"                        jsonschema:"description=The command to execute. For shell commands: any valid sh command. For pseudo-commands: pass them directly here (e.g. scan -i 192.168.1.0/24 --mode quick). Pseudo-commands are built into this tool."`
	Background         bool   `json:"background,omitempty"           jsonschema:"description=Run the command as a background task and return immediately with a task_id. Use for long-running commands. Use the task tool to peek/list/wait/kill."`
	TaskName           string `json:"task_name,omitempty"            jsonschema:"description=Optional human label for the background task (shown by task list). Ignored when background=false."`
	TaskTimeoutSeconds int    `json:"task_timeout_seconds,omitempty" jsonschema:"description=Optional wall-clock kill deadline for the background task. Defaults to 1800 (30 min). Ignored when background=false."`
	Filename           string `json:"filename,omitempty"             jsonschema:"description=Optional file path to save task output. When set stdout is written to both memory and the file. Ignored when background=false."`
}

func (t *BashTool) Definition() provider.ToolDefinition {
	return ToolDef("bash", t.Description(), BashArgs{})
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (ToolResult, error) {
	args, err := ParseArgs[BashArgs](arguments)
	if err != nil {
		return ToolResult{}, err
	}

	cmdLine := strings.TrimSpace(args.Command)
	if cmdLine == "" {
		return ToolResult{}, fmt.Errorf("empty command")
	}

	if isOnlyCommentsOrBlank(cmdLine) {
		return TextResult("ok"), nil
	}

	var pseudoCleaned string
	var isPseudo bool
	if t.registry != nil {
		pseudoCleaned, isPseudo = extractPseudoCommand(cmdLine, t.registry)
	}

	if args.Background {
		if isPseudo {
			return t.execPseudoBackground(pseudoCleaned, args.TaskName, args.TaskTimeoutSeconds, args.Filename)
		}
		return t.execBackground(cmdLine, args.TaskName, args.TaskTimeoutSeconds, args.Filename)
	}

	if isPseudo {
		output, err := t.registry.Execute(ctx, pseudoCleaned)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(output), nil
	}

	return t.execShell(ctx, cmdLine)
}

// execPseudoBackground delegates a pseudo-command (scan/gogo/neutron/...)
// to the task manager so it runs in the background while the agent keeps
// working. Output is streamed (or returned at end) to the task's stdout
// file; the completion notification is injected into the agent's inbox.
func (t *BashTool) execPseudoBackground(cmdLine, name string, timeoutSeconds int, filename string) (ToolResult, error) {
	timeout := time.Duration(timeoutSeconds) * time.Second
	reg := t.registry
	fn := func(ctx context.Context, w io.Writer) error {
		tokens, err := SplitCommandLine(cmdLine)
		if err != nil {
			return err
		}
		out, err := reg.ExecuteArgsStreaming(ctx, tokens, w)
		if err != nil {
			return err
		}
		if out != "" {
			_, _ = io.WriteString(w, out)
		}
		return nil
	}
	opt := task.SpawnOption{Filename: filename}
	info, err := t.tasks.SpawnInProcess(name, cmdLine, timeout, fn, opt)
	if err != nil {
		return ToolResult{}, err
	}
	msg := fmt.Sprintf(
		"Started pseudo-command task id=%s name=%s (in-process)\nUse `task peek %s` to inspect progress, `task wait %s` to block, `task kill %s` to stop.",
		info.ID, info.Name, info.ID, info.ID, info.ID)
	if info.Filename != "" {
		msg += fmt.Sprintf("\nOutput also saved to: %s", info.Filename)
	}
	return TextResult(msg), nil
}

const autoBackgroundThreshold = 15 * time.Second

func (t *BashTool) execShell(ctx context.Context, cmdLine string) (ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeout)*time.Second)
	defer cancel()

	cmd := task.ShellCommand(cmdLine)
	cmd.Dir = t.workDir
	t.applyProxyEnv(cmd)
	configureProcessGroup(cmd)

	buf := task.NewOutputBuffer(task.DefaultBufferCap)
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return ToolResult{}, err
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case err := <-exited:
		return t.collectForegroundResult(buf, err, ctx)

	case <-time.After(autoBackgroundThreshold):
		remaining := time.Duration(t.timeout)*time.Second - autoBackgroundThreshold
		if remaining <= 0 {
			remaining = task.DefaultTimeout
		}
		info := t.tasks.Adopt(cmd, buf, labelFromCommand(cmdLine), remaining)
		return TextResult(fmt.Sprintf(
			"Command auto-backgrounded (exceeded %s).\ntask id=%s name=%s\nUse `task peek %s` to check progress.",
			autoBackgroundThreshold, info.ID, info.Name, info.ID)), nil

	case <-ctx.Done():
		_ = terminateProcess(cmd)
		select {
		case <-exited:
		case <-time.After(foregroundStopWait):
			_ = killProcess(cmd)
			<-exited
		}
		return t.collectForegroundResult(buf, ctx.Err(), ctx)
	}
}

func (t *BashTool) collectForegroundResult(buf *task.OutputBuffer, err error, ctx context.Context) (ToolResult, error) {
	output := buf.String()
	if len(output) > maxOutputSize {
		original := len(output)
		output = output[:maxOutputSize] + fmt.Sprintf(
			"\n\n[truncated: showing %d of %d bytes]", maxOutputSize, original)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return TextResult(output), fmt.Errorf("command timed out after %ds", t.timeout)
		}
		if output == "" {
			return ToolResult{}, err
		}
		return TextResult(output + "\n[exit code: non-zero]"), nil
	}
	return TextResult(output), nil
}

func labelFromCommand(cmdLine string) string {
	cmdLine = strings.TrimSpace(cmdLine)
	if i := strings.IndexAny(cmdLine, " \t\n"); i > 0 {
		cmdLine = cmdLine[:i]
	}
	return cmdLine
}

// execBackground delegates to the task manager; the agent gets back a task
// id immediately and can keep working while the command runs to completion.
func (t *BashTool) execBackground(cmdLine, name string, timeoutSeconds int, filename string) (ToolResult, error) {
	timeout := time.Duration(timeoutSeconds) * time.Second
	opt := task.SpawnOption{Filename: filename}
	info, err := t.tasks.Spawn(t.workDir, cmdLine, name, timeout, opt)
	if err != nil {
		return ToolResult{}, err
	}
	msg := fmt.Sprintf(
		"Started background task id=%s name=%s pid=%d\nUse `task peek %s` to inspect progress, `task wait %s` to block, `task kill %s` to stop. A completion message will be injected automatically when the task ends.",
		info.ID, info.Name, info.PID, info.ID, info.ID, info.ID)
	if info.Filename != "" {
		msg += fmt.Sprintf("\nOutput also saved to: %s", info.Filename)
	}
	return TextResult(msg), nil
}

// isOnlyCommentsOrBlank returns true if every line in the input is blank or a
// shell comment (starts with '#' after trimming). LLMs occasionally emit
// comment-only "commands" which should be treated as no-ops.
func isOnlyCommentsOrBlank(cmdLine string) bool {
	for _, line := range strings.Split(cmdLine, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

// extractPseudoCommand preprocesses a (possibly multi-line, commented) command
// string from the LLM and checks whether any segment is a registered pseudo-command.
// It strips comment lines (lines whose first non-space character is '#'), blank lines,
// and splits on '&&' / ';' to inspect each segment individually.
// Returns the cleaned pseudo-command line and true if found, or ("", false) otherwise.
func extractPseudoCommand(cmdLine string, registry *CommandRegistry) (string, bool) {
	// 1. Strip comment lines and blank lines
	lines := strings.Split(cmdLine, "\n")
	var meaningful []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		meaningful = append(meaningful, line)
	}
	if len(meaningful) == 0 {
		return "", false
	}

	// Rejoin the non-comment lines
	cleaned := strings.Join(meaningful, "\n")

	// 2. Quick check: is the whole thing a single pseudo-command?
	token := firstCommandToken(cleaned)
	if token != "" && registry.Has(token) {
		return cleaned, true
	}

	// 3. Split on && and ; to check each segment
	// Replace && and ; with a common delimiter, then split
	segments := splitShellChain(cleaned)
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		token := firstCommandToken(seg)
		if token != "" && registry.Has(token) {
			return seg, true
		}
	}

	return "", false
}

// splitShellChain splits a command string on '&&' and ';' delimiters,
// respecting quoted strings. Returns individual command segments.
func splitShellChain(input string) []string {
	var segments []string
	var cur strings.Builder
	var quote rune
	escaped := false
	runes := []rune(input)

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			cur.WriteRune(r)
			continue
		}
		if quote != 0 {
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			cur.WriteRune(r)
			continue
		}
		// Check for &&
		if r == '&' && i+1 < len(runes) && runes[i+1] == '&' {
			segments = append(segments, cur.String())
			cur.Reset()
			i++ // skip second '&'
			continue
		}
		// Check for ;
		if r == ';' {
			segments = append(segments, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

func (t *BashTool) applyProxyEnv(cmd *exec.Cmd) {
	if t.scannerProxy == "" {
		return
	}
	env := os.Environ()
	env = append(env,
		"ALL_PROXY="+t.scannerProxy,
		"all_proxy="+t.scannerProxy,
		"HTTP_PROXY="+t.scannerProxy,
		"http_proxy="+t.scannerProxy,
		"HTTPS_PROXY="+t.scannerProxy,
		"https_proxy="+t.scannerProxy,
	)
	cmd.Env = env
}

func firstCommandToken(input string) string {
	input = strings.TrimSpace(input)
	var sb strings.Builder
	var quote rune
	escaped := false
	for _, r := range input {
		if escaped {
			sb.WriteRune(r)
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
				continue
			}
			sb.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			break
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
