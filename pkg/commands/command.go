package commands

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
)

var _ tool.Executor = (*CommandRegistry)(nil)

// Command describes one built-in command accepted by the Bash tool. Runtime
// state belongs to Execution; command-specific dependencies are captured by
// Run at construction time.
type Command struct {
	Name            string
	Usage           string
	QuickReference  string
	Run             func(context.Context, *Execution) (any, error)
	SetProxy        func(string)
	GetProxy        func() string
	SetDefaultSpace func(string)
	Close           func()
}

type LoggerAware interface {
	InitLogger(telemetry.Logger)
}

type CommandRegistry struct {
	mu     sync.RWMutex
	items  map[string]Command
	order  []string
	groups map[string][]string

	tools     map[string]tool.Tool
	toolOrder []string
}

func (r *CommandRegistry) SetLogger(logger telemetry.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tool := range r.tools {
		if aware, ok := tool.(LoggerAware); ok {
			aware.InitLogger(logger)
		}
	}
}

func NewRegistry() *CommandRegistry {
	return &CommandRegistry{
		items:  make(map[string]Command),
		groups: make(map[string][]string),
		tools:  make(map[string]tool.Tool),
	}
}

func (r *CommandRegistry) RegisterTool(t tool.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, exists := r.tools[name]; !exists {
		r.toolOrder = append(r.toolOrder, name)
	}
	r.tools[name] = t
}

func (r *CommandRegistry) Tools() []tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]tool.Tool, 0, len(r.toolOrder))
	for _, name := range r.toolOrder {
		result = append(result, r.tools[name])
	}
	return result
}

func (r *CommandRegistry) GetTool(name string) (tool.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *CommandRegistry) ToolDefinitions() []tool.Definition {
	tools := r.Tools()
	defs := make([]tool.Definition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

func (r *CommandRegistry) ExecuteTool(ctx context.Context, name, arguments string) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = tool.Result{}
			err = fmt.Errorf("tool %s panic: %v\n%s", name, recovered, debug.Stack())
		}
	}()

	t, ok := r.GetTool(name)
	if !ok {
		return tool.Result{}, fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, arguments)
}

func (r *CommandRegistry) Register(cmd Command, group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := cmd.Name
	if _, exists := r.items[name]; !exists {
		r.order = append(r.order, name)
	}
	r.items[name] = cmd
	if group != "" {
		r.groups[group] = append(r.groups[group], name)
	}
}

func (r *CommandRegistry) Get(name string) (Command, bool) {
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

func (r *CommandRegistry) All() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Command, 0, len(r.order))
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

// Run executes a nested built-in command inside an existing Execution. The
// child shares the outer PTY session and file descriptors; it does not create
// a second lifecycle or an ID-less execution.
func (r *CommandRegistry) Run(ctx context.Context, tokens []string, parent *Execution) (details any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			details = nil
			err = fmt.Errorf("command panic: %v\n%s", recovered, debug.Stack())
		}
	}()

	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	name := tokens[0]
	cmd, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown command: %s", name)
	}

	args, parseErr := stripShellSyntax(tokens[1:])
	if parseErr != nil {
		return nil, parseErr
	}
	args = normalizeNoColor(name, args)

	if parent == nil {
		return nil, fmt.Errorf("command %s requires an execution", name)
	}
	if cmd.Run == nil {
		return nil, fmt.Errorf("command %s has no runner", name)
	}
	child := &Execution{
		ID: parent.ID, Command: name, Args: args, Dir: parent.Dir, Env: parent.Env,
		Stdin: parent.Stdin, Stdout: parent.Stdout, Stderr: parent.Stderr,
		State: parent.State, ExitCode: parent.ExitCode, StartedAt: parent.StartedAt,
		EndedAt: parent.EndedAt, KillCause: parent.KillCause, manager: parent.manager,
	}
	return cmd.Run(ctx, child)
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

func normalizeNoColor(name string, args []string) []string {
	if name != "scan" {
		return args
	}
	for _, a := range args {
		if a == "--no-color" {
			return args
		}
	}
	return append(args, "--no-color")
}

func (r *CommandRegistry) UsageDocs() string {
	var sb strings.Builder
	for _, cmd := range r.All() {
		if cmd.QuickReference != "" {
			sb.WriteString(cmd.QuickReference)
			sb.WriteString("\n")
			continue
		}
		first := cmd.Usage
		if idx := strings.IndexByte(first, '\n'); idx > 0 {
			first = first[:idx]
		}
		first = strings.TrimSpace(first)
		if !strings.HasPrefix(first, cmd.Name) {
			first = cmd.Name
		}
		sb.WriteString("- ")
		sb.WriteString(first)
		sb.WriteString("\n")
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

// JoinCommandLine builds a command line that SplitCommandLine can losslessly
// parse. It is intended for trusted internal callers that already have args.
func JoinCommandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteCommandArg(name))
	for _, arg := range args {
		parts = append(parts, quoteCommandArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \\t\\r\\n\\\"'\\\\|;&<>") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\\\''") + "'"
}
