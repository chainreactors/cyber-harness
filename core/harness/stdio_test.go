//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
)

func TestConsumeAgentStream(t *testing.T) {
	input := encodeEvents(t,
		aopTestEvent("root", aop.TypeSessionStart, aop.SessionStartData{}),
		aopTestEvent("root", aop.TypeText, aop.TextData{Content: "hello", Role: "assistant"}),
		aopTestEvent("root", aop.TypeSessionEnd, aop.SessionEndData{Stop: "completed"}),
	)

	var monitorOutput bytes.Buffer
	output, events, err := consumeAgentStream(input, NewMonitor(&monitorOutput))
	if err != nil {
		t.Fatalf("consumeAgentStream() error = %v", err)
	}
	if output != "hello" || len(events) != 3 {
		t.Fatalf("output=%q events=%#v", output, events)
	}
	if !strings.Contains(monitorOutput.String(), "hello") {
		t.Fatalf("monitor output = %q", monitorOutput.String())
	}
}

func TestConsumeAgentStreamKeepsTypedToolData(t *testing.T) {
	callID := "call-1"
	input := encodeEvents(t,
		aopTestEvent("root", aop.TypeSessionStart, aop.SessionStartData{}),
		aopTestEvent("root", aop.TypeToolCall, aop.ToolCallData{
			ToolCallID: callID,
			ToolName:   "bash",
			Args: map[string]any{
				"command": "echo hello",
				"nested":  []any{map[string]any{"enabled": true}},
			},
		}),
		aopTestEvent("root", aop.TypeToolResult, aop.ToolResultData{
			ToolCallID: callID,
			ToolName:   "bash",
			Content:    map[string]any{"output": []any{"hello", map[string]any{"code": float64(0)}}},
		}),
		aopTestEvent("root", aop.TypeSessionEnd, aop.SessionEndData{Stop: "completed"}),
	)

	_, events, err := consumeAgentStream(input, nil)
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

func TestConsumeAgentStreamWaitsForRootSessionEnd(t *testing.T) {
	input := encodeEvents(t,
		aopTestEvent("root", aop.TypeSessionStart, aop.SessionStartData{}),
		aopTestEvent("child", aop.TypeSessionStart, aop.SessionStartData{ParentSessionID: "root"}),
		aopTestEvent("child", aop.TypeSessionEnd, aop.SessionEndData{Stop: "completed"}),
		aopTestEvent("root", aop.TypeText, aop.TextData{Content: "root done", Role: "assistant"}),
		aopTestEvent("root", aop.TypeSessionEnd, aop.SessionEndData{Stop: "completed"}),
	)
	output, events, err := consumeAgentStream(input, nil)
	if err != nil || output != "root done" || len(events) != 5 {
		t.Fatalf("output=%q events=%d err=%v", output, len(events), err)
	}
}

func TestConsumeAgentStreamReportsRootError(t *testing.T) {
	input := encodeEvents(t,
		aopTestEvent("root", aop.TypeSessionStart, aop.SessionStartData{}),
		aopTestEvent("root", aop.TypeError, aop.ErrorData{Message: "provider failed"}),
		aopTestEvent("root", aop.TypeSessionEnd, aop.SessionEndData{Stop: "error", Error: "provider failed"}),
	)
	_, events, err := consumeAgentStream(input, nil)
	if err == nil || !strings.Contains(err.Error(), "provider failed") || len(events) != 3 {
		t.Fatalf("events=%d error=%v", len(events), err)
	}
}

func TestConsumeAgentStreamRejectsInvalidStreams(t *testing.T) {
	tests := []struct {
		name   string
		input  *bytes.Buffer
		needle string
	}{
		{
			name:   "invalid envelope",
			input:  encodeRaw(t, map[string]any{"type": "text"}),
			needle: "invalid AOP envelope",
		},
		{
			name: "missing root terminal",
			input: encodeEvents(t,
				aopTestEvent("root", aop.TypeSessionStart, aop.SessionStartData{}),
				aopTestEvent("root", aop.TypeText, aop.TextData{Content: "hello"}),
			),
			needle: "without root session.end",
		},
		{
			name:   "no root session",
			input:  encodeEvents(t, aopTestEvent("child", aop.TypeText, aop.TextData{Content: "hello"})),
			needle: "without a root session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := consumeAgentStream(tt.input, nil)
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("error = %v, want containing %q", err, tt.needle)
			}
		})
	}
}

func aopTestEvent(sessionID, eventType string, data any) aop.Event {
	raw, _ := json.Marshal(data)
	return aop.Event{
		Type:      eventType,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: sessionID,
		Agent:     "aiscan",
		Data:      raw,
	}
}

func encodeEvents(t *testing.T, events ...aop.Event) *bytes.Buffer {
	t.Helper()
	values := make([]any, len(events))
	for i := range events {
		values[i] = events[i]
	}
	return encodeRaw(t, values...)
}

func encodeRaw(t *testing.T, values ...any) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	return &output
}
