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
	event := aopTestEvent(aop.TypeText, aop.TextData{Content: "hello", Role: "assistant"})
	input := encodeFrames(t,
		webproto.Message{
			Type:    "aop",
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
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	text, err := aop.DecodeData[aop.TextData](events[0])
	if err != nil || text.Content != "hello" {
		t.Fatalf("text event = %#v err=%v", events[0], err)
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
			Type:   "aop",
			TaskID: taskID,
			Payload: webproto.MustJSON(aopTestEvent(aop.TypeToolCall, aop.ToolCallData{
				ToolCallID: callID,
				ToolName:   "bash",
				Args: map[string]any{
					"command": "echo hello",
					"nested":  []any{map[string]any{"enabled": true}},
				},
			})),
		},
		webproto.Message{
			Type:   "aop",
			TaskID: taskID,
			Payload: webproto.MustJSON(aopTestEvent(aop.TypeToolResult, aop.ToolResultData{
				ToolCallID: callID,
				ToolName:   "bash",
				Content:    map[string]any{"output": []any{"hello", map[string]any{"code": float64(0)}}},
			})),
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
	args, ok := calls[0].Args().(map[string]any)
	if !ok || args["command"] != "echo hello" {
		t.Fatalf("args = %#v", calls[0].Args())
	}
	if !strings.Contains(calls[0].ResultText(), `"output":["hello"`) {
		t.Fatalf("result = %q", calls[0].ResultText())
	}
}

func TestConsumeAgentStreamRejectsInvalidFrames(t *testing.T) {
	taskID := "task-1"
	validEvent := aopTestEvent(aop.TypeText, aop.TextData{Content: "hello"})
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
				webproto.Message{Type: "aop", TaskID: taskID, Payload: webproto.MustJSON(validEvent)},
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
			name:   "missing terminal",
			input:  encodeFrames(t, webproto.Message{Type: "aop", TaskID: taskID, Payload: webproto.MustJSON(validEvent)}),
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

func aopTestEvent(eventType string, data any) aop.Event {
	return aop.Event{
		Type:      eventType,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "session-1",
		Agent:     "aiscan",
		Data:      webproto.MustJSON(data),
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
