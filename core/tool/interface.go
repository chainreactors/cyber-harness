package tool

import (
	"context"
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
)

// Tool is a single tool that an LLM agent can invoke.
type Tool interface {
	Name() string
	Description() string
	Definition() *aop.ToolDefinition
	Execute(ctx context.Context, arguments string) (*Result, error)
}

// Executor is the minimal interface the agent loop needs to
// discover and invoke tools. CommandRegistry satisfies it directly.
type Executor interface {
	ToolDefinitions() []*aop.ToolDefinition
	ExecuteTool(ctx context.Context, name, arguments string) (*Result, error)
}

// EmptyExecutor returns an Executor with no tools.
func EmptyExecutor() Executor { return emptyExec{} }

type emptyExec struct{}

func (emptyExec) ToolDefinitions() []*aop.ToolDefinition { return nil }
func (emptyExec) ExecuteTool(_ context.Context, name, _ string) (*Result, error) {
	return nil, fmt.Errorf("unknown tool: %s", name)
}
