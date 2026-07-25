package webagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type aopTestExecutor struct{}

func (aopTestExecutor) ExecuteTool(_ context.Context, name, arguments string) (tool.Result, error) {
	return tool.TextResult(name + ":" + arguments), nil
}

func toolCommand(toolCallID, toolName string, args map[string]any) aop.ToolCallData {
	return aop.ToolCallData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
	}
}

func decodeCommandResult(t *testing.T, msg webproto.Message) webproto.CommandResultPayload {
	t.Helper()
	if msg.Type != webproto.TypeCommandResult {
		t.Fatalf("result envelope = %+v", msg)
	}
	var result webproto.CommandResultPayload
	if err := json.Unmarshal(msg.Payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHandleToolCommand(t *testing.T) {
	var got webproto.Message
	HandleToolCommand(context.Background(), webproto.Message{Type: webproto.TypeCommand, TaskID: "call-1"},
		toolCommand("call-1", "echo", map[string]any{"value": "hello"}),
		aopTestExecutor{}, nil, func(msg webproto.Message) { got = msg })

	if got.TaskID != "call-1" {
		t.Fatalf("result envelope = %+v", got)
	}
	result := decodeCommandResult(t, got)
	if result.Metadata["tool_call_id"] != "call-1" || result.Metadata["tool_name"] != "echo" || len(result.Parts) != 1 {
		t.Fatalf("result data = %+v", result)
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

// TestHandleToolCommandForeground verifies that a foreground-capable tool is
// run via RunForegroundTool, that output lines stream as tool.data progress
// events correlated by the call session id, and that the tool.result carries
// the text content plus structured Details.
func TestHandleToolCommandForeground(t *testing.T) {
	reg := commands.NewRegistry()
	bash := &recordingBash{}
	reg.RegisterTool(bash)

	dataBus := eventbus.New[output.ToolDataEvent]()
	var progress []output.ToolDataEvent
	dataBus.Subscribe(func(ev output.ToolDataEvent) {
		if ev.Kind == output.ToolDataProgress {
			progress = append(progress, ev)
		}
	})

	var got webproto.Message
	HandleToolCommand(context.Background(), webproto.Message{Type: webproto.TypeCommand, TaskID: "task-1"},
		toolCommand("call-1", "bash", map[string]any{"command": "echo test", "timeout": 7}),
		reg, dataBus, func(msg webproto.Message) { got = msg })

	if bash.command != "echo test" || bash.options.Timeout != 7*time.Second {
		t.Fatalf("bash options = %+v", bash.options)
	}
	if len(progress) != 1 || progress[0].Data != "streamed" || progress[0].CallID != "task-1" || progress[0].Tool != "bash" {
		t.Fatalf("progress events = %+v", progress)
	}

	result := decodeCommandResult(t, got)
	if isError, _ := result.Metadata["is_error"].(bool); isError || len(result.Parts) == 0 || result.Parts[0].Text != "streamed" {
		t.Fatalf("result data = %+v", result)
	}
	details, _ := json.Marshal(result.Metadata["details"])
	var structured output.Result
	if err := json.Unmarshal(details, &structured); err != nil {
		t.Fatalf("decode structured details: %v", err)
	}
	if structured.Summary.Targets != 2 {
		t.Fatalf("structured details = %+v", structured)
	}
}
