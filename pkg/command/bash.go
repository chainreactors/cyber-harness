package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/provider"
)

const (
	maxOutputSize                = 50 * 1024 // 50KB
	defaultTimeout               = 300       // 5 minutes
	backgroundStartupWait        = 300 * time.Millisecond
	backgroundStartupOutputLimit = 8 * 1024
)

type BashTool struct {
	registry    *CommandRegistry
	workDir     string
	timeout     int
	bgMu        sync.Mutex
	bgProcesses map[int]*exec.Cmd
}

func NewBashTool(workDir string, timeout int, registry *CommandRegistry) *BashTool {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &BashTool{
		registry:    registry,
		workDir:     workDir,
		timeout:     timeout,
		bgProcesses: make(map[int]*exec.Cmd),
	}
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
	for _, cmd := range t.backgroundCommands() {
		_ = terminateBackgroundProcess(cmd)
	}
	if t.waitForBackgroundProcesses(time.Second) {
		return
	}
	for _, cmd := range t.backgroundCommands() {
		_ = killBackgroundProcess(cmd)
	}
	_ = t.waitForBackgroundProcesses(time.Second)
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
						"description": "Start a normal shell command in the background and return a PID after a short startup check. Useful for long-running services like SSH tunnels (use ssh -o ExitOnForwardFailure=yes -L ... -N). On Unix, use 'kill -0 -- -<pid>' to check the process group and 'kill -- -<pid>' to stop it. Scanner pseudo-commands still run in the foreground.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Command    string `json:"command"`
		Background bool   `json:"background"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	cmdLine := strings.TrimSpace(args.Command)
	if cmdLine == "" {
		return "", fmt.Errorf("empty command")
	}

	if t.registry != nil {
		firstToken := firstCommandToken(cmdLine)
		if firstToken != "" && t.registry.Has(firstToken) {
			return t.registry.Execute(ctx, cmdLine)
		}
	}

	if args.Background {
		return t.execBackground(ctx, cmdLine)
	}

	return t.execShell(ctx, cmdLine)
}

func (t *BashTool) execShell(ctx context.Context, cmdLine string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeout)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", cmdLine)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdLine)
	}
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var sb strings.Builder
	if stdout.Len() > 0 {
		sb.Write(stdout.Bytes())
	}
	if stderr.Len() > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("[stderr] ")
		sb.Write(stderr.Bytes())
	}

	output := sb.String()
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

func (t *BashTool) execBackground(ctx context.Context, cmdLine string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", cmdLine)
	} else {
		cmd = exec.Command("sh", "-c", cmdLine)
	}
	cmd.Dir = t.workDir
	cmd.Stdin = nil
	configureBackgroundCommand(cmd)
	stdout := newLimitedBuffer(backgroundStartupOutputLimit)
	stderr := newLimitedBuffer(backgroundStartupOutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start background process: %w", err)
	}

	pid := cmd.Process.Pid
	t.bgMu.Lock()
	t.bgProcesses[pid] = cmd
	t.bgMu.Unlock()

	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		t.bgMu.Lock()
		delete(t.bgProcesses, pid)
		t.bgMu.Unlock()
		exited <- err
	}()

	select {
	case err := <-exited:
		return "", backgroundStartupError(err, stdout.String(), stderr.String())
	case <-time.After(backgroundStartupWait):
		select {
		case err := <-exited:
			return "", backgroundStartupError(err, stdout.String(), stderr.String())
		default:
		}
	case <-ctx.Done():
		stopStartedBackgroundProcess(cmd, exited)
		return "", ctx.Err()
	}

	return fmt.Sprintf("Background process started with PID %d. Use '%s' to check if running, '%s' to stop.", pid, backgroundStatusCommand(pid), backgroundStopCommand(pid)), nil
}

func stopStartedBackgroundProcess(cmd *exec.Cmd, exited <-chan error) {
	_ = terminateBackgroundProcess(cmd)
	select {
	case <-exited:
		return
	case <-time.After(time.Second):
	}
	_ = killBackgroundProcess(cmd)
	select {
	case <-exited:
	case <-time.After(time.Second):
	}
}

func (t *BashTool) backgroundCommands() []*exec.Cmd {
	t.bgMu.Lock()
	defer t.bgMu.Unlock()

	cmds := make([]*exec.Cmd, 0, len(t.bgProcesses))
	for _, cmd := range t.bgProcesses {
		cmds = append(cmds, cmd)
	}
	return cmds
}

func (t *BashTool) waitForBackgroundProcesses(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		t.bgMu.Lock()
		count := len(t.bgProcesses)
		t.bgMu.Unlock()
		if count == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func backgroundStartupError(err error, stdout, stderr string) error {
	output := formatBackgroundOutput(stdout, stderr)
	if err == nil {
		if output == "" {
			return fmt.Errorf("background process exited during startup")
		}
		return fmt.Errorf("background process exited during startup:\n%s", output)
	}
	if output == "" {
		return fmt.Errorf("background process exited during startup: %w", err)
	}
	return fmt.Errorf("background process exited during startup: %w\n%s", err, output)
}

func formatBackgroundOutput(stdout, stderr string) string {
	var sb strings.Builder
	if stdout != "" {
		sb.WriteString(stdout)
	}
	if stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("[stderr] ")
		sb.WriteString(stderr)
	}
	return strings.TrimSpace(sb.String())
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.buf.Len() < b.limit {
		remaining := b.limit - b.buf.Len()
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
			b.truncated = true
			return len(p), nil
		}
		b.buf.Write(p)
		return len(p), nil
	}
	if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	output := b.buf.String()
	if b.truncated {
		output += fmt.Sprintf("\n[truncated: showing first %d bytes]", b.limit)
	}
	return output
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
