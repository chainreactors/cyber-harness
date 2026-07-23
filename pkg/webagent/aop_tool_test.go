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

func aopToolCallMessage(t *testing.T, taskID, toolCallID, toolName string, args map[string]any) webproto.Message {
	t.Helper()
	callData, _ := json.Marshal(aop.ToolCallData{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
	})
	call := aop.Event{
		Type: aop.TypeToolCall, TS: "2026-07-21T00:00:00Z",
		SessionID: taskID, Agent: "aiscan.web", Data: callData,
	}
	payload, _ := json.Marshal(call)
	return webproto.Message{Type: "aop", TaskID: taskID, Payload: payload}
}

func decodeToolResult(t *testing.T, msg webproto.Message) (aop.Event, aop.ToolResultData) {
	t.Helper()
	if msg.Type != "aop" {
		t.Fatalf("result envelope = %+v", msg)
	}
	var event aop.Event
	if err := json.Unmarshal(msg.Payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != aop.TypeToolResult {
		t.Fatalf("result event = %+v", event)
	}
	var result aop.ToolResultData
	if err := json.Unmarshal(event.Data, &result); err != nil {
		t.Fatal(err)
	}
	return event, result
}

func TestHandleAOPToolCall(t *testing.T) {
	var got webproto.Message
	HandleAOPToolCall(context.Background(),
		aopToolCallMessage(t, "call-1", "call-1", "echo", map[string]any{"value": "hello"}),
		aopTestExecutor{}, nil, func(msg webproto.Message) { got = msg })

	if got.TaskID != "call-1" {
		t.Fatalf("result envelope = %+v", got)
	}
	event, result := decodeToolResult(t, got)
	if event.SessionID != "call-1" || event.Agent != "aiscan.web" {
		t.Fatalf("result event = %+v", event)
	}
	if result.ToolCallID != "call-1" || result.ToolName != "echo" || result.IsError {
		t.Fatalf("result data = %+v", result)
	}
}

type recordingBash struct {
	command string
	options commands.BashExecOptions
}

func (*recordingBash) Name() string        { return "bash" }
func (*recordingBash) Description() string { return "test bash" }
func (*recordingBash) Definition() commands.ToolDefinition {
	return commands.ToolDef("bash", "test bash", struct {
		Command string `json:"command"`
	}{})
}
func (*recordingBash) Execute(context.Context, string) (commands.ToolResult, error) {
	return commands.ToolResult{}, nil
}
func (b *recordingBash) RunForegroundTool(_ context.Context, command string, options commands.BashExecOptions) (commands.ToolResult, error) {
	b.command = command
	b.options = options
	options.OnOutput([]byte("streamed\n"))
	result := commands.TextResult("streamed")
	result.Details = &output.Result{Summary: output.Summary{Targets: 2}}
	return result, nil
}

// TestHandleAOPToolCallForeground verifies that a foreground-capable tool is
// run via RunForegroundTool, that output lines stream as tool.data progress
// events correlated by the call session id, and that the tool.result carries
// the text content plus structured Details.
func TestHandleAOPToolCallForeground(t *testing.T) {
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
	HandleAOPToolCall(context.Background(),
		aopToolCallMessage(t, "task-1", "call-1", "bash", map[string]any{"command": "echo test", "timeout": 7}),
		reg, dataBus, func(msg webproto.Message) { got = msg })

	if bash.command != "echo test" || bash.options.Timeout != 7*time.Second {
		t.Fatalf("bash options = %+v", bash.options)
	}
	if len(progress) != 1 || progress[0].Data != "streamed" || progress[0].CallID != "task-1" || progress[0].Tool != "bash" {
		t.Fatalf("progress events = %+v", progress)
	}

	event, result := decodeToolResult(t, got)
	if event.SessionID != "task-1" {
		t.Fatalf("result event = %+v", event)
	}
	if result.IsError || result.Content != "streamed" {
		t.Fatalf("result data = %+v", result)
	}
	details, _ := json.Marshal(result.Details)
	var structured output.Result
	if err := json.Unmarshal(details, &structured); err != nil {
		t.Fatalf("decode structured details: %v", err)
	}
	if structured.Summary.Targets != 2 {
		t.Fatalf("structured details = %+v", structured)
	}
}
