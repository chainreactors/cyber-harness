package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
)

type deniedCommandExecutor struct {
	calls                      int
	name, arguments, sessionID string
}

func (*deniedCommandExecutor) ToolDefinitions() []*aop.ToolDefinition { return nil }
func (e *deniedCommandExecutor) ExecuteTool(ctx context.Context, name, arguments string) (*aop.ToolResult, error) {
	e.calls++
	e.name = name
	e.arguments = arguments
	e.sessionID = operation.InvocationFromContext(ctx).SessionID
	return nil, operation.ErrDenied
}

func TestDirectCommandCannotBypassToolExecutor(t *testing.T) {
	runtime := newBareRuntime(t, nil, nil)
	executor := &deniedCommandExecutor{}
	runtime.config.Tools = executor
	session, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "guardrail-command"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Command(t.Context(), "!echo guardrail-inert-probe")
	if !errors.Is(err, operation.ErrDenied) {
		t.Fatalf("direct command bypassed admission: %v", err)
	}
	var args map[string]string
	if err = json.Unmarshal([]byte(executor.arguments), &args); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || executor.name != "bash" || executor.sessionID != "guardrail-command" || args["command"] != "echo guardrail-inert-probe" {
		t.Fatal("direct command lost executor or invocation context")
	}
}
