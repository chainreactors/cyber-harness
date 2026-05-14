package command

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/provider"
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

type CommandRegistry struct {
	mu     sync.RWMutex
	items  map[string]PseudoCommand
	order  []string
	groups map[string][]string

	tools      map[string]AgentTool
	toolOrder  []string
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

func (r *CommandRegistry) ExecuteTool(ctx context.Context, name, arguments string) (string, error) {
	t, ok := r.GetTool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, arguments)
}

func (r *CommandRegistry) Register(cmd PseudoCommand, group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := cmd.Name()
	if _, exists := r.items[name]; !exists {
		r.order = append(r.order, name)
	}
	r.items[name] = cmd
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

	args := tokens[1:]
	if stream != nil {
		if streaming, ok := cmd.(StreamingCommand); ok {
			return streaming.ExecuteStreaming(ctx, args, stream)
		}
	}
	return cmd.Execute(ctx, args)
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
