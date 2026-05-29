package command

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
)

const (
	maxOutputSize          = 50 * 1024
	defaultTimeout         = 300
	autoBackgroundThreshold = 15 * time.Second
	foregroundStopWait     = 5 * time.Second
)

type BashTool struct {
	registry     *CommandRegistry
	workDir      string
	timeout      int
	scannerProxy string
	tasks        *tmux.Manager
	selfBinary   string
}

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
		tasks:    tmux.NewManager(),
	}
}

func (t *BashTool) Manager() *tmux.Manager { return t.tasks }

func (t *BashTool) WithScannerProxy(proxy string) *BashTool {
	t.scannerProxy = proxy
	return t
}

func (t *BashTool) SetScannerProxy(proxy string) { t.scannerProxy = proxy }
func (t *BashTool) SetSelfBinary(path string)    { t.selfBinary = path }
func (t *BashTool) Name() string                 { return "bash" }
func (t *BashTool) Close()                       { t.tasks.Shutdown() }

func (t *BashTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.registry != nil {
		names := t.registry.Names()
		if len(names) > 0 {
			desc += " IMPORTANT: This tool also handles pseudo-commands (" + strings.Join(names, ", ") + "). Pass them as the command parameter."
		}
	}
	return desc
}

type BashArgs struct {
	Command string `json:"command" jsonschema:"description=The command to execute. For shell commands: any valid sh command. For pseudo-commands (scan, gogo, tmux, etc.): pass them directly here."`
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

	// Check for pseudo-command
	if t.registry != nil {
		if cleaned, isPseudo := extractPseudoCommand(cmdLine, t.registry); isPseudo {
			token := firstCommandToken(cleaned)
			if cmd, ok := t.registry.Get(token); ok {
				if _, inProc := cmd.(InProcessCommand); inProc {
					result, err := t.registry.Execute(ctx, cleaned)
					if err != nil {
						return ToolResult{}, err
					}
					return TextResult(result), nil
				}
			}
			// Scanner pseudo-command: run as subprocess with auto-background
			return t.execForeground(ctx, cleaned, true)
		}
	}

	return t.execForeground(ctx, cmdLine, false)
}

// execForeground creates a session and waits for completion. If the command
// exceeds autoBackgroundThreshold, it returns immediately with the session ID.
func (t *BashTool) execForeground(ctx context.Context, cmdLine string, isPseudo bool) (ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeout)*time.Second)
	defer cancel()

	var info tmux.Info
	var err error

	if isPseudo {
		tokens, parseErr := SplitCommandLine(cmdLine)
		if parseErr != nil {
			return ToolResult{}, parseErr
		}
		if len(tokens) > 1 {
			if _, valErr := stripShellSyntax(tokens[1:]); valErr != nil {
				return ToolResult{}, valErr
			}
		}
		self := t.selfBinary
		if self == "" {
			self, err = os.Executable()
			if err != nil {
				return ToolResult{}, fmt.Errorf("resolve self: %w", err)
			}
		}
		subArgs := append(tokens, "--no-color")
		env := t.buildSubprocessEnv()
		info, err = t.tasks.CreateCmd(t.workDir, self, subArgs, "", time.Duration(t.timeout)*time.Second, env, "")
	} else {
		info, err = t.tasks.Create(t.workDir, cmdLine, "", time.Duration(t.timeout)*time.Second, t.proxyEnv(), "")
	}
	if err != nil {
		return ToolResult{}, err
	}

	done := t.tasks.Done(info.ID)
	select {
	case <-done:
		return t.collectResult(info.ID, ctx), nil
	case <-time.After(autoBackgroundThreshold):
		return TextResult(fmt.Sprintf(
			"Command auto-backgrounded (exceeded %s).\nsession id=%s name=%s\nUse `tmux peek -t %s` to check progress, `tmux kill -t %s` to stop.",
			autoBackgroundThreshold, info.ID, info.Name, info.ID, info.ID)), nil
	case <-ctx.Done():
		_ = t.tasks.Kill(info.ID)
		<-done
		return t.collectResult(info.ID, ctx), nil
	}
}

func (t *BashTool) collectResult(id string, ctx context.Context) ToolResult {
	output := t.tasks.PeekOrEmpty(id, 0)
	if len(output) > maxOutputSize {
		original := len(output)
		output = output[:maxOutputSize] + fmt.Sprintf("\n\n[truncated: showing %d of %d bytes]", maxOutputSize, original)
	}
	if ctx.Err() != nil {
		output += fmt.Sprintf("\n[command timed out after %ds]", t.timeout)
	}
	info, _ := t.tasks.Get(id)
	if info.ExitCode != 0 && info.State != tmux.StateRunning {
		output += fmt.Sprintf("\n[exit code: %d]", info.ExitCode)
	}
	return TextResult(output)
}

func (t *BashTool) proxyEnv() []string {
	if t.scannerProxy == "" {
		return nil
	}
	return []string{
		"ALL_PROXY=" + t.scannerProxy,
		"all_proxy=" + t.scannerProxy,
		"HTTP_PROXY=" + t.scannerProxy,
		"http_proxy=" + t.scannerProxy,
		"HTTPS_PROXY=" + t.scannerProxy,
		"https_proxy=" + t.scannerProxy,
	}
}

func (t *BashTool) buildSubprocessEnv() []string {
	env := t.proxyEnv()
	env = append(env, "AISCAN_PARENT=1")
	return env
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

func extractPseudoCommand(cmdLine string, registry *CommandRegistry) (string, bool) {
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
	cleaned := strings.Join(meaningful, "\n")
	token := firstCommandToken(cleaned)
	if token != "" && registry.Has(token) {
		return cleaned, true
	}
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
		if r == '&' && i+1 < len(runes) && runes[i+1] == '&' {
			segments = append(segments, cur.String())
			cur.Reset()
			i++
			continue
		}
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
