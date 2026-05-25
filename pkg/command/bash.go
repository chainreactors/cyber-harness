package command

import (
"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"runtime"
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

func (t *BashTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        "bash",
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The command to execute. For shell commands: any valid sh command. For pseudo-commands: pass them directly here (e.g. 'scan -i 192.168.1.0/24 --mode quick', 'gogo -i 10.0.0.0/24 -p top100', 'spray -u http://target --finger'). Pseudo-commands are built into this tool — do NOT try to run them as standalone tools or system binaries.",
					},
					"background": map[string]any{
						"type":        "boolean",
						"description": "Run the command as a background task and return immediately with a task_id. Use this for any long-running command (scan, gogo with many targets, neutron, ssh tunnels). The agent loop keeps working while the task runs; you'll receive a follow-up message when it completes. Use the `task` tool to peek/list/wait/kill. Scanner pseudo-commands still execute in-process when background=false.",
					},
					"task_name": map[string]any{
						"type":        "string",
						"description": "Optional human label for the background task (shown by `task list`). Ignored when background=false.",
					},
					"task_timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Optional wall-clock kill deadline for the background task. Defaults to 1800 (30 min). Ignored when background=false.",
					},
					"filename": map[string]any{
						"type":        "string",
						"description": "Optional file path to save task output. When set, stdout is written to both memory and the file. When omitted, output stays in memory only (use `task peek` to read). Ignored when background=false.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Command            string `json:"command"`
		Background         bool   `json:"background"`
		TaskName           string `json:"task_name"`
		TaskTimeoutSeconds int    `json:"task_timeout_seconds"`
		Filename           string `json:"filename"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	cmdLine := strings.TrimSpace(args.Command)
	if cmdLine == "" {
		return "", fmt.Errorf("empty command")
	}

	// If the command is entirely comments/blank lines, nothing to run.
	if isOnlyCommentsOrBlank(cmdLine) {
		return "ok", nil
	}

	// Check for pseudo-command up-front so we can route both background and
	// foreground modes through the right path. A pseudo-command in
	// background mode runs inside the task manager as an in-process
	// goroutine; foreground mode runs synchronously as before.
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
		// Foreground pseudo-commands are intentionally not wrapped in the bash
		// tool's shell timeout. Scanner commands manage their own request-level
		// timeouts, and long whole-task runs should be launched with
		// background:true instead.
		return t.registry.Execute(ctx, pseudoCleaned)
	}

	return t.execShell(ctx, cmdLine)
}

// execPseudoBackground delegates a pseudo-command (scan/gogo/neutron/...)
// to the task manager so it runs in the background while the agent keeps
// working. Output is streamed (or returned at end) to the task's stdout
// file; the completion notification is injected into the agent's inbox.
func (t *BashTool) execPseudoBackground(cmdLine, name string, timeoutSeconds int, filename string) (string, error) {
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
		return "", err
	}
	msg := fmt.Sprintf(
		"Started pseudo-command task id=%s name=%s (in-process)\nUse `task peek %s` to inspect progress, `task wait %s` to block, `task kill %s` to stop.",
		info.ID, info.Name, info.ID, info.ID, info.ID)
	if info.Filename != "" {
		msg += fmt.Sprintf("\nOutput also saved to: %s", info.Filename)
	}
	return msg, nil
}

const autoBackgroundThreshold = 15 * time.Second

func (t *BashTool) execShell(ctx context.Context, cmdLine string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeout)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", cmdLine)
	} else {
		cmd = exec.Command("sh", "-c", cmdLine)
	}
	cmd.Dir = t.workDir
	t.applyProxyEnv(cmd)
	configureProcessGroup(cmd)

	buf := task.NewOutputBuffer(task.DefaultBufferCap)
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return "", err
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
		return fmt.Sprintf(
			"Command auto-backgrounded (exceeded %s).\ntask id=%s name=%s\nUse `task peek %s` to check progress.",
			autoBackgroundThreshold, info.ID, info.Name, info.ID), nil

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

func (t *BashTool) collectForegroundResult(buf *task.OutputBuffer, err error, ctx context.Context) (string, error) {
	output := buf.String()
	if len(output) > maxOutputSize {
		original := len(output)
		output = output[:maxOutputSize] + fmt.Sprintf(
			"\n\n[truncated: showing %d of %d bytes]", maxOutputSize, original)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("command timed out after %ds", t.timeout)
		}
		if output == "" {
			return "", err
		}
		return output + "\n[exit code: non-zero]", nil
	}
	return output, nil
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
func (t *BashTool) execBackground(cmdLine, name string, timeoutSeconds int, filename string) (string, error) {
	timeout := time.Duration(timeoutSeconds) * time.Second
	opt := task.SpawnOption{Filename: filename}
	info, err := t.tasks.Spawn(t.workDir, cmdLine, name, timeout, opt)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf(
		"Started background task id=%s name=%s pid=%d\nUse `task peek %s` to inspect progress, `task wait %s` to block, `task kill %s` to stop. A completion message will be injected automatically when the task ends.",
		info.ID, info.Name, info.PID, info.ID, info.ID, info.ID)
	if info.Filename != "" {
		msg += fmt.Sprintf("\nOutput also saved to: %s", info.Filename)
	}
	return msg, nil
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
