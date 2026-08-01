package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
)

type aopTestExecutor struct{}

func (aopTestExecutor) ExecuteTool(_ context.Context, name, arguments string) (tool.Result, error) {
	return tool.TextResult(name + ":" + arguments), nil
}

type structuredResultExecutor struct {
	err error
}

func (e structuredResultExecutor) ExecuteTool(context.Context, string, string) (tool.Result, error) {
	return tool.Result{
		Content: []tool.ContentBlock{
			tool.TextBlock("partial"),
			tool.ImageBlock("image/png", base64.StdEncoding.EncodeToString([]byte("image"))),
		},
		Details:   map[string]any{"ports": float64(3)},
		IsError:   e.err == nil,
		Terminate: true,
	}, e.err
}

func TestExecuteToolRequestPreservesStructuredResult(t *testing.T) {
	event, err := executeToolRequest(context.Background(), toolRequest(t, "call-structured", "scan", nil), structuredResultExecutor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if !result.IsError || !result.Terminate || result.DurationMs > uint64(time.Minute.Milliseconds()) {
		t.Fatalf("result flags = %+v", result)
	}
	if len(result.Output) != 2 || result.Output[0].GetText().GetText() != "partial" || string(result.Output[1].GetMedia().GetResource().GetData()) != "image" {
		t.Fatalf("result output = %+v", result.Output)
	}
	detail, err := aop.DecodeJSON[map[string]float64](result.Detail)
	if err != nil || detail["ports"] != 3 {
		t.Fatalf("detail = %+v, err=%v", detail, err)
	}
}

func TestExecuteToolRequestUsesExecutionErrorText(t *testing.T) {
	event, err := executeToolRequest(context.Background(), toolRequest(t, "call-error", "scan", nil), structuredResultExecutor{err: errors.New("failed")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if !result.IsError || result.Output[0].GetText().GetText() != "failed" {
		t.Fatalf("result = %+v", result)
	}
}

func toolRequest(t *testing.T, id, name string, arguments map[string]any) *transport.ToolCallRequest {
	t.Helper()
	value, err := aop.JSONValue(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return &transport.ToolCallRequest{TaskId: id, SessionId: "session-1", TurnId: "turn-1", Call: &aop.ToolCall{Id: id, Name: name, Arguments: value}}
}

func TestExecuteToolRequest(t *testing.T) {
	event, err := executeToolRequest(context.Background(), toolRequest(t, "call-1", "echo", map[string]any{"value": "hello"}), aopTestExecutor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if result.CallId != "call-1" || result.Name != "echo" || !strings.Contains(result.Output[0].GetText().Text, "echo") {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteToolRequestRejectsMismatchedCorrelation(t *testing.T) {
	request := toolRequest(t, "call-1", "echo", map[string]any{"value": "hello"})
	request.TaskId = "other"
	if _, err := executeToolRequest(context.Background(), request, aopTestExecutor{}, nil); err == nil {
		t.Fatal("expected correlation error")
	}
}

type recordingBash struct {
	command string
	options commands.BashExecOptions
}

func (*recordingBash) Name() string        { return "bash" }
func (*recordingBash) Description() string { return "test bash" }
func (*recordingBash) Definition() tool.Definition {
	return tool.Def("bash", "test bash", struct {
		Command string `json:"command"`
	}{})
}
func (*recordingBash) Execute(context.Context, string) (tool.Result, error) {
	return tool.Result{}, nil
}
func (b *recordingBash) RunForegroundTool(_ context.Context, command string, options commands.BashExecOptions) (tool.Result, error) {
	b.command = command
	b.options = options
	options.OnOutput([]byte("streamed\n"))
	result := tool.TextResult("streamed")
	result.Details = &output.Result{Summary: output.Summary{Targets: 2}}
	return result, nil
}

func TestExecuteToolRequestForeground(t *testing.T) {
	registry := commands.NewRegistry()
	bash := &recordingBash{}
	registry.RegisterTool(bash)
	dataBus := eventbus.New[output.ToolDataEvent]()
	var progress []output.ToolDataEvent
	dataBus.Subscribe(func(event output.ToolDataEvent) {
		if event.Kind == output.ToolDataProgress {
			progress = append(progress, event)
		}
	})
	event, err := executeToolRequest(context.Background(), toolRequest(t, "task-1", "bash", map[string]any{"command": "echo test", "timeout": 7}), registry, dataBus)
	if err != nil {
		t.Fatal(err)
	}
	if bash.command != "echo test" || bash.options.Timeout != 7*time.Second {
		t.Fatalf("bash options = %+v", bash.options)
	}
	if len(progress) != 1 || progress[0].Data != "streamed" || progress[0].CallID != "task-1" {
		t.Fatalf("progress = %+v", progress)
	}
	result := event.GetToolResult()
	if result.IsError || result.Output[0].GetText().Text != "streamed" {
		t.Fatalf("result = %+v", result)
	}
	structured, err := aop.DecodeJSON[output.Result](result.Detail)
	if err != nil || structured.Summary.Targets != 2 {
		t.Fatalf("detail = %+v, err=%v", structured, err)
	}
}
