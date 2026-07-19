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
	result, err := consumeAgentStream(input, taskID, NewMonitor(&monitorOutput))
	if err != nil {
		t.Fatalf("consumeAgentStream() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q", result.Output)
	}
	if len(result.Events) != 1 || result.Events[0].Content != "hello" {
		t.Fatalf("events = %#v", result.Events)
	}
	if !strings.Contains(monitorOutput.String(), "hello") {
		t.Fatalf("monitor output = %q", monitorOutput.String())
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
			needle: "args must be an object",
		},
		{
			name:   "missing terminal",
			input:  encodeFrames(t, webproto.Message{Type: "aop.text", TaskID: taskID, Payload: webproto.MustJSON(validEvent)}),
			needle: "without a terminal frame",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := consumeAgentStream(tt.input, taskID, nil)
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
