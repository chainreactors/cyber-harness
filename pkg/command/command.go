package command

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

type PseudoCommand interface {
	Name() string
	Usage() string
	Execute(ctx context.Context, args []string) (string, error)
}

type StreamingCommand interface {
	PseudoCommand
	ExecuteStreaming(ctx context.Context, args []string, stream io.Writer) (string, error)
}

type AgentTool interface {
	Name() string
	Description() string
	Definition() provider.ToolDefinition
	Execute(ctx context.Context, arguments string) (string, error)
}

type ToolContext struct {
	SystemPrompt string
	Messages     []provider.ChatMessage
}

type ContextAwareAgentTool interface {
	AgentTool
	ExecuteWithContext(ctx context.Context, arguments string, toolCtx ToolContext) (string, error)
}

type WorkDirAware interface {
	SetWorkDir(dir string)
}

type CommandRegistry struct {
	mu      sync.RWMutex
	items   map[string]PseudoCommand
	order   []string
	groups  map[string][]string
	workDir string

	tools     map[string]AgentTool
	toolOrder []string
}

func NewRegistry() *CommandRegistry {
	return &CommandRegistry{
		items:  make(map[string]PseudoCommand),
		groups: make(map[string][]string),
		tools:  make(map[string]AgentTool),
	}
}

func (r *CommandRegistry) RegisterTool(t AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, exists := r.tools[name]; !exists {
		r.toolOrder = append(r.toolOrder, name)
	}
	r.tools[name] = t
}

func (r *CommandRegistry) Tools() []AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AgentTool, 0, len(r.toolOrder))
	for _, name := range r.toolOrder {
		result = append(result, r.tools[name])
	}
	return result
}

func (r *CommandRegistry) GetTool(name string) (AgentTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *CommandRegistry) ToolDefinitions() []provider.ToolDefinition {
	tools := r.Tools()
	defs := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

func (r *CommandRegistry) ExecuteTool(ctx context.Context, name, arguments string, toolCtx ...ToolContext) (string, error) {
	t, ok := r.GetTool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if ca, ok := t.(ContextAwareAgentTool); ok && len(toolCtx) > 0 {
		return ca.ExecuteWithContext(ctx, arguments, toolCtx[0])
	}
	return t.Execute(ctx, arguments)
}

func (r *CommandRegistry) SetWorkDir(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workDir = dir
	for _, cmd := range r.items {
		if wda, ok := cmd.(WorkDirAware); ok {
			wda.SetWorkDir(dir)
		}
	}
}

func (r *CommandRegistry) Register(cmd PseudoCommand, group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := cmd.Name()
	if _, exists := r.items[name]; !exists {
		r.order = append(r.order, name)
	}
	r.items[name] = cmd
	if r.workDir != "" {
		if wda, ok := cmd.(WorkDirAware); ok {
			wda.SetWorkDir(r.workDir)
		}
	}
	if group != "" {
		r.groups[group] = append(r.groups[group], name)
	}
}

func (r *CommandRegistry) Get(name string) (PseudoCommand, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.items[name]
	return cmd, ok
}

func (r *CommandRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.items[name]
	return ok
}

func (r *CommandRegistry) All() []PseudoCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]PseudoCommand, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.items[name])
	}
	return result
}

func (r *CommandRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

func (r *CommandRegistry) GroupNames(group string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.groups[group]...)
}

func (r *CommandRegistry) Execute(ctx context.Context, cmdLine string) (string, error) {
	tokens, err := SplitCommandLine(cmdLine)
	if err != nil {
		return "", err
	}
	return r.ExecuteArgs(ctx, tokens)
}

func (r *CommandRegistry) ExecuteArgs(ctx context.Context, tokens []string) (string, error) {
	return r.ExecuteArgsStreaming(ctx, tokens, nil)
}

func (r *CommandRegistry) ExecuteArgsStreaming(ctx context.Context, tokens []string, stream io.Writer) (out string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = ""
			err = fmt.Errorf("command panic: %v\n%s", recovered, debug.Stack())
		}
	}()

	if len(tokens) == 0 {
		return "", fmt.Errorf("empty command")
	}

	name := tokens[0]
	cmd, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown command: %s", name)
	}

	args, err := stripShellSyntax(tokens[1:])
	if err != nil {
		return "", err
	}
	if stream != nil {
		if streaming, ok := cmd.(StreamingCommand); ok {
			return streaming.ExecuteStreaming(ctx, args, stream)
		}
	}
	return cmd.Execute(ctx, args)
}

// stripShellSyntax processes shell-style tokens that LLMs frequently append
// to pseudo-command invocations. Pseudo-commands run in-process and have no
// shell to interpret these, so we either strip the inert ones or reject the
// command outright when the LLM's intent would be silently lost.
//
// Silently stripped (no semantic loss for in-process execution):
//   - stderr/stdout duplication: 2>&1, 1>&2, >&2, &>
//
// Rejected with a clear error so the LLM rewrites its next call:
//   - Pipes (|, ||): the LLM expects output filtering (e.g. "| head -30")
//     to limit a scanner's run. Silently dropping the pipe makes the
//     scanner run to completion against the full wordlist, which is the
//     deadlock we want to prevent.
//   - File redirections (>file, >>file, <file, 2>file, 1>file): the LLM
//     expects output to be written somewhere it can read back. Stripping
//     leaves the file uncreated.
//   - Command chaining (&&, ;): tokens after these belong to a separate
//     command the LLM intends to run, not to the pseudo-command.
func stripShellSyntax(tokens []string) ([]string, error) {
	clean := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "|" || t == "||" {
			return nil, fmt.Errorf("pseudo-commands run in-process and do not support shell pipes (got %q). To limit output, use the scanner's own flags (e.g. spray --limit, gogo -p with a smaller port list) or call a separate filter step in a follow-up bash command", t)
		}
		if t == "&&" || t == ";" {
			return nil, fmt.Errorf("pseudo-commands do not support shell command chaining (got %q). Issue each command in a separate bash tool call", t)
		}
		if isStderrDup(t) {
			continue
		}
		if isFileRedirection(t) {
			return nil, fmt.Errorf("pseudo-commands do not support file redirection (got %q). They run in-process and return their output as the tool result; capture it from the result text instead", t)
		}
		clean = append(clean, t)
	}
	return clean, nil
}

// isStderrDup reports whether the token is a stderr/stdout duplication that
// has no effect for in-process execution and can be silently stripped.
// Note: "&>" is intentionally not here — it always targets a file, so it
// belongs in isFileRedirection.
func isStderrDup(token string) bool {
	switch token {
	case "2>&1", "1>&2", ">&2", ">&1":
		return true
	}
	return false
}

// isFileRedirection reports whether the token is a shell redirection that
// the LLM intends to actually divert output to/from a file. These must be
// rejected rather than stripped, because stripping silently breaks the
// LLM's mental model of where the output ends up.
func isFileRedirection(token string) bool {
	// Standalone operators (file comes as the next token).
	switch token {
	case ">", ">>", "<", "<<", "2>", "1>", "0<", "&>", "&>>":
		return true
	}
	// Glued forms like >file, 2>file, &>/dev/null.
	for _, prefix := range []string{
		"&>", "2>", "1>", "0<", ">>", ">", "<<", "<",
	} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func (r *CommandRegistry) UsageDocs() string {
	var sb strings.Builder
	for _, cmd := range r.All() {
		sb.WriteString("```\n")
		sb.WriteString(cmd.Usage())
		sb.WriteString("\n```\n\n")
	}
	return sb.String()
}

func SplitCommandLine(input string) ([]string, error) {
	// Pre-process: strip comment-only lines and blank lines so that
	// LLM-generated preambles like "# scanning target\nscan -i ..." work.
	lines := strings.Split(input, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	input = strings.Join(kept, " ")

	var tokens []string
	var cur strings.Builder
	var quote rune
	escaped := false

	for _, r := range input {
		if escaped {
			switch r {
			case '\\', '\'', '"', ' ', '\t', '\n', '\r':
				cur.WriteRune(r)
			default:
				cur.WriteRune('\\')
				cur.WriteRune(r)
			}
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
			cur.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}

	if escaped {
		cur.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}
