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
	input := encodeFrames(t,
		sessionOpenedFrame("root"),
		aopFrame(aopTestEvent("root", "", aop.TypeSessionStart, aop.SessionStartData{})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnStart, aop.TurnStartData{})),
		aopFrame(aopMessageEvent("root", "turn-1", "assistant", "hello")),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnEnd, aop.TurnEndData{Stop: "completed"})),
	)

	var monitorOutput bytes.Buffer
	output, events, err := consumeAgentStream(input, NewMonitor(&monitorOutput))
	if err != nil {
		t.Fatalf("consumeAgentStream() error = %v", err)
	}
	if output != "hello" || len(events) != 4 {
		t.Fatalf("output=%q events=%#v", output, events)
	}
	if !strings.Contains(monitorOutput.String(), "hello") || !strings.Contains(monitorOutput.String(), "run turn-1") {
		t.Fatalf("monitor output = %q", monitorOutput.String())
	}
}

func TestConsumeAgentStreamKeepsTypedToolData(t *testing.T) {
	callID := "call-1"
	input := encodeFrames(t,
		sessionOpenedFrame("root"),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnStart, aop.TurnStartData{})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeToolCall, aop.ToolCallData{
			ToolCallID: callID,
			ToolName:   "bash",
			Args: map[string]any{
				"command": "echo hello",
				"nested":  []any{map[string]any{"enabled": true}},
			},
		})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeToolResult, aop.ToolResultData{
			ToolCallID: callID,
			ToolName:   "bash",
			Content:    map[string]any{"output": []any{"hello", map[string]any{"code": float64(0)}}},
		})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnEnd, aop.TurnEndData{Stop: "completed"})),
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

func TestConsumeAgentStreamWaitsForRootRunEnd(t *testing.T) {
	input := encodeFrames(t,
		sessionOpenedFrame("root"),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnStart, aop.TurnStartData{})),
		aopFrame(aopTestEvent("child", "child-turn", aop.TypeTurnStart, aop.TurnStartData{})),
		aopFrame(aopTestEvent("child", "child-turn", aop.TypeTurnEnd, aop.TurnEndData{Stop: "completed"})),
		aopFrame(aopMessageEvent("root", "turn-1", "assistant", "root done")),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnEnd, aop.TurnEndData{Stop: "completed"})),
	)
	output, events, err := consumeAgentStream(input, nil)
	if err != nil || output != "root done" || len(events) != 5 {
		t.Fatalf("output=%q events=%d err=%v", output, len(events), err)
	}
}

func TestConsumeAgentStreamReportsRootError(t *testing.T) {
	input := encodeFrames(t,
		sessionOpenedFrame("root"),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnStart, aop.TurnStartData{})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeError, aop.ErrorData{Message: "provider failed"})),
		aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnEnd, aop.TurnEndData{Stop: "error", Error: "provider failed"})),
	)
	_, events, err := consumeAgentStream(input, nil)
	if err == nil || !strings.Contains(err.Error(), "provider failed") || len(events) != 3 {
		t.Fatalf("events=%d error=%v", len(events), err)
	}
}

func TestConsumeAgentStreamReportsProtocolError(t *testing.T) {
	input := encodeFrames(t, webproto.Message{
		Type:    webproto.TypeError,
		Payload: webproto.MustJSON(webproto.ErrorPayload{Message: "session rejected"}),
	})
	_, _, err := consumeAgentStream(input, nil)
	if err == nil || !strings.Contains(err.Error(), "session rejected") {
		t.Fatalf("error = %v", err)
	}
}

func TestConsumeAgentStreamRejectsInvalidStreams(t *testing.T) {
	tests := []struct {
		name   string
		input  *bytes.Buffer
		needle string
	}{
		{
			name: "invalid envelope",
			input: encodeFrames(t,
				sessionOpenedFrame("root"),
				webproto.Message{Type: webproto.TypeAOP, Payload: webproto.MustJSON(map[string]any{"type": "text"})},
			),
			needle: "invalid AOP envelope",
		},
		{
			name: "missing run terminal",
			input: encodeFrames(t,
				sessionOpenedFrame("root"),
				aopFrame(aopTestEvent("root", "turn-1", aop.TypeTurnStart, aop.TurnStartData{})),
				aopFrame(aopMessageEvent("root", "turn-1", "assistant", "hello")),
			),
			needle: "without run turn.end",
		},
		{
			name:   "no opened session",
			input:  encodeFrames(t),
			needle: "without session.opened",
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

func aopTestEvent(sessionID, turnID, eventType string, data any) aop.Event {
	raw, _ := json.Marshal(data)
	return aop.Event{
		Type:      eventType,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: sessionID,
		TurnID:    turnID,
		Agent:     "aiscan",
		Data:      raw,
	}
}

func aopMessageEvent(sessionID, turnID, role, text string) aop.Event {
	return aopTestEvent(sessionID, turnID, aop.TypeMessage, aop.MessageData{
		MessageID: "m-1",
		Role:      role,
		Parts:     []aop.MessagePart{{Type: aop.PartText, Text: text}},
	})
}

func sessionOpenedFrame(sessionID string) webproto.Message {
	return webproto.Message{
		Type:    webproto.TypeSessionOpened,
		Payload: webproto.MustJSON(webproto.SessionLifecyclePayload{SessionID: sessionID}),
	}
}

func aopFrame(event aop.Event) webproto.Message {
	return webproto.Message{Type: webproto.TypeAOP, TurnID: event.TurnID, Payload: webproto.MustJSON(event)}
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
