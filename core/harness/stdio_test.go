//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestConsumeAgentStream(t *testing.T) {
	taskID := "task-1"
	event := aop.Event{
		V:    aop.Version,
		Type: aop.TypeText,
		Data: webproto.MustJSON(aop.TextData{Content: "hello", Role: "assistant"}),
	}
	input := encodeFrames(t,
		webproto.Message{
			Type:    "aop.text",
			TaskID:  taskID,
			Payload: webproto.MustJSON(event),
		},
		webproto.Message{Type: "complete", TaskID: taskID, Data: "done"},
	)

	var monitorOutput bytes.Buffer
	output, events, err := consumeAgentStream(input, taskID, NewMonitor(&monitorOutput))
	if err != nil {
		t.Fatalf("consumeAgentStream() error = %v", err)
	}
	if output != "done" {
		t.Fatalf("output = %q", output)
	}
	if len(events) != 1 || events[0].Content != "hello" {
		t.Fatalf("events = %#v", events)
	}
	if !strings.Contains(monitorOutput.String(), "hello") {
		t.Fatalf("monitor output = %q", monitorOutput.String())
	}
}

func TestConsumeAgentStreamKeepsTypedToolData(t *testing.T) {
	taskID := "task-1"
	callID := "call-1"
	input := encodeFrames(t,
		webproto.Message{
			Type:   "aop.tool.call",
			TaskID: taskID,
			Payload: webproto.MustJSON(aop.Event{
				V:    aop.Version,
				Type: aop.TypeToolCall,
				Data: webproto.MustJSON(aop.ToolCallData{
					ToolCallID: callID,
					ToolName:   "bash",
					Args:       map[string]any{"command": "echo hello"},
				}),
			}),
		},
		webproto.Message{
			Type:   "aop.tool.result",
			TaskID: taskID,
			Payload: webproto.MustJSON(aop.Event{
				V:    aop.Version,
				Type: aop.TypeToolResult,
				Data: webproto.MustJSON(aop.ToolResultData{
					ToolCallID: callID,
					ToolName:   "bash",
					Content:    "hello",
				}),
			}),
		},
		webproto.Message{Type: "complete", TaskID: taskID, Data: "done"},
	)

	_, events, err := consumeAgentStream(input, taskID, nil)
	if err != nil {
		t.Fatalf("consumeAgentStream() error = %v", err)
	}
	calls := (&RunResult{Events: events}).ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v", calls)
	}
	if string(calls[0].Args["command"]) != `"echo hello"` {
		t.Fatalf("args = %#v", calls[0].Args)
	}
	if calls[0].Result != "hello" {
		t.Fatalf("result = %q", calls[0].Result)
	}
}

func TestConsumeAgentStreamRejectsInvalidFrames(t *testing.T) {
	taskID := "task-1"
	validEvent := aop.Event{
		V:    aop.Version,
		Type: aop.TypeText,
		Data: webproto.MustJSON(aop.TextData{Content: "hello"}),
	}
	invalidToolArgs := aop.Event{
		V:    aop.Version,
		Type: aop.TypeToolCall,
		Data: webproto.MustJSON(aop.ToolCallData{
			ToolCallID: "call-1",
			ToolName:   "bash",
			Args:       "echo hello",
		}),
	}
	invalidToolResult := aop.Event{
		V:    aop.Version,
		Type: aop.TypeToolResult,
		Data: webproto.MustJSON(aop.ToolResultData{
			ToolCallID: "call-1",
			ToolName:   "bash",
			Content:    map[string]any{"output": "hello"},
		}),
	}

	tests := []struct {
		name   string
		input  *bytes.Buffer
		needle string
	}{
		{
			name: "task mismatch",
			input: encodeFrames(t,
				webproto.Message{Type: "complete", TaskID: "other"},
			),
			needle: "task_id",
		},
		{
			name: "event type mismatch",
			input: encodeFrames(t,
				webproto.Message{Type: "aop.usage", TaskID: taskID, Payload: webproto.MustJSON(validEvent)},
			),
			needle: "contains AOP event",
		},
		{
			name: "unknown frame",
			input: encodeFrames(t,
				webproto.Message{Type: "log", TaskID: taskID},
			),
			needle: "unsupported webproto frame",
		},
		{
			name: "non-object tool args",
			input: encodeFrames(t,
				webproto.Message{Type: "aop.tool.call", TaskID: taskID, Payload: webproto.MustJSON(invalidToolArgs)},
			),
			needle: "decode AOP tool.call data",
		},
		{
			name: "non-string tool result",
			input: encodeFrames(t,
				webproto.Message{Type: "aop.tool.result", TaskID: taskID, Payload: webproto.MustJSON(invalidToolResult)},
			),
			needle: "decode AOP tool.result data",
		},
		{
			name:   "missing terminal",
			input:  encodeFrames(t, webproto.Message{Type: "aop.text", TaskID: taskID, Payload: webproto.MustJSON(validEvent)}),
			needle: "without a terminal frame",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := consumeAgentStream(tt.input, taskID, nil)
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("error = %v, want containing %q", err, tt.needle)
			}
		})
	}
}

func encodeFrames(t *testing.T, messages ...webproto.Message) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, message := range messages {
		if err := encoder.Encode(message); err != nil {
			t.Fatal(err)
		}
	}
	return &output
}
