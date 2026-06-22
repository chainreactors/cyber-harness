package commands

import (
	"context"
	"strings"
	"testing"
)

// panicTool is a test tool that always panics.
type panicTool struct{ msg string }

func (t *panicTool) Name() string            { return "panic_tool" }
func (t *panicTool) Description() string      { return "always panics" }
func (t *panicTool) Definition() ToolDefinition { return ToolDefinition{} }
func (t *panicTool) Execute(_ context.Context, _ string) (ToolResult, error) {
	panic(t.msg)
}

func TestExecuteTool_RecoversPanic(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterTool(&panicTool{msg: "boom"})

	result, err := reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from panicking tool, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should contain panic message, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "tool panic_tool panic") {
		t.Fatalf("error should identify the tool, got: %s", err.Error())
	}
	if result.Text() != "" {
		t.Fatalf("result should be empty on panic, got: %s", result.Text())
	}
}

// normalTool returns a result without panicking.
type normalTool struct{}

func (t *normalTool) Name() string            { return "normal_tool" }
func (t *normalTool) Description() string      { return "works fine" }
func (t *normalTool) Definition() ToolDefinition { return ToolDefinition{} }
func (t *normalTool) Execute(_ context.Context, _ string) (ToolResult, error) {
	return TextResult("hello"), nil
}

func TestExecuteTool_NormalToolUnaffected(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterTool(&normalTool{})

	result, err := reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text() != "hello" {
		t.Fatalf("expected 'hello', got: %s", result.Text())
	}
}

func TestExecuteTool_PanicDoesNotAffectSubsequentCalls(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterTool(&panicTool{msg: "crash"})
	reg.RegisterTool(&normalTool{})

	// Call 1: panics — should be recovered and returned as error.
	_, err := reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from panicking tool")
	}
	t.Logf("call 1 (panic_tool): recovered panic → err=%v", err)

	// Call 2: normal tool after the panic — must succeed.
	result, err := reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("normal tool failed after panic recovery: %v", err)
	}
	if result.Text() != "hello" {
		t.Fatalf("expected 'hello', got: %s", result.Text())
	}
	t.Logf("call 2 (normal_tool): succeeded after panic → result=%q", result.Text())

	// Call 3: panic again — still recoverable.
	_, err = reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from second panicking call")
	}
	t.Logf("call 3 (panic_tool): recovered again → err=%v", err)

	// Call 4: normal tool still works after repeated panics.
	result, err = reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("normal tool failed after second panic: %v", err)
	}
	t.Logf("call 4 (normal_tool): still works → result=%q", result.Text())
}
