package aop

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

func TestFromAgentEventToolCall(t *testing.T) {
	ev := agent.Event{
		Type:       agent.EventToolExecutionStart,
		SessionID:  "sess-001",
		Turn:       1,
		ToolCallID: "call_abc",
		ToolName:   "bash",
		Arguments:  `{"command":"ls -la"}`,
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != TypeToolCall {
		t.Errorf("type = %s, want %s", e.Type, TypeToolCall)
	}
	if !e.Valid() {
		t.Fatal("event envelope is invalid")
	}
	if e.Agent != "aiscan" {
		t.Errorf("agent = %s, want aiscan", e.Agent)
	}

	var data ToolCallData
	if err := json.Unmarshal(e.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.ToolName != "bash" {
		t.Errorf("tool_name = %s, want bash", data.ToolName)
	}
	if data.ToolCallID != "call_abc" {
		t.Errorf("tool_call_id = %s, want call_abc", data.ToolCallID)
	}
	argsMap, ok := data.Args.(map[string]any)
	if !ok {
		t.Fatalf("args is not a map: %T", data.Args)
	}
	if argsMap["command"] != "ls -la" {
		t.Errorf("args.command = %v, want ls -la", argsMap["command"])
	}
}

func TestFromAgentEventToolResult(t *testing.T) {
	ev := agent.Event{
		Type:       agent.EventToolExecutionEnd,
		SessionID:  "sess-001",
		Turn:       1,
		ToolCallID: "call_abc",
		ToolName:   "bash",
		Result:     "total 128\ndrwxr-xr-x ...",
		IsError:    false,
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != TypeToolResult {
		t.Errorf("type = %s, want %s", events[0].Type, TypeToolResult)
	}

	var data ToolResultData
	json.Unmarshal(events[0].Data, &data)
	if data.Content != "total 128\ndrwxr-xr-x ..." {
		t.Errorf("content = %q", data.Content)
	}
	if data.IsError {
		t.Error("is_error should be false")
	}
}

func TestFromAgentEventMessageEnd(t *testing.T) {
	content := "Scan complete."
	ev := agent.Event{
		Type:      agent.EventMessageEnd,
		SessionID: "sess-001",
		Turn:      1,
		Message: agent.ChatMessage{
			Role:    "assistant",
			Content: &content,
		},
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != TypeText {
		t.Errorf("type = %s, want %s", events[0].Type, TypeText)
	}

	var data TextData
	json.Unmarshal(events[0].Data, &data)
	if data.Content != "Scan complete." {
		t.Errorf("content = %q", data.Content)
	}
	if data.Role != "assistant" {
		t.Errorf("role = %q, want assistant", data.Role)
	}
	if data.Delta {
		t.Error("delta should be false for message_end")
	}
}

func TestFromAgentEventMessageUpdateUsesAppendOnlyFragment(t *testing.T) {
	cumulative := "hello"
	ev := agent.Event{
		Type:         agent.EventMessageUpdate,
		ContentDelta: "lo",
		Message: agent.ChatMessage{
			Role:    "assistant",
			Content: &cumulative,
		},
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	var data TextData
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.Content != "lo" {
		t.Fatalf("content = %q, want append-only fragment %q", data.Content, "lo")
	}
	if !data.Delta {
		t.Fatal("delta should be true for message_update")
	}
}

func TestFromAgentEventTurnEndProducesTwo(t *testing.T) {
	ev := agent.Event{
		Type:      agent.EventTurnEnd,
		SessionID: "sess-001",
		Turn:      2,
		Usage: &provider.Usage{
			PromptTokens:     1500,
			CompletionTokens: 200,
			TotalTokens:      1700,
			CacheReadTokens:  500,
		},
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) != 2 {
		t.Fatalf("expected 2 events (turn.end + usage), got %d", len(events))
	}
	if events[0].Type != TypeTurnEnd {
		t.Errorf("events[0].type = %s, want %s", events[0].Type, TypeTurnEnd)
	}
	if events[1].Type != TypeUsage {
		t.Errorf("events[1].type = %s, want %s", events[1].Type, TypeUsage)
	}

	var usage UsageData
	json.Unmarshal(events[1].Data, &usage)
	if usage.InputTokens != 1500 {
		t.Errorf("input_tokens = %d, want 1500", usage.InputTokens)
	}
	if usage.OutputTokens != 200 {
		t.Errorf("output_tokens = %d, want 200", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 500 {
		t.Errorf("cache_read_tokens = %d, want 500", usage.CacheReadTokens)
	}
}

func TestFromAgentEventEvalExt(t *testing.T) {
	ev := agent.Event{
		Type:       agent.EventTurnEnd,
		SessionID:  "sess-001",
		Turn:       1,
		EvalRound:  2,
		EvalPass:   true,
		EvalReason: "target scanned",
	}

	events := FromAgentEvent(ev, "aiscan")
	if len(events) == 0 {
		t.Fatal("expected at least 1 event")
	}
	ext, ok := events[0].Ext["aiscan"]
	if !ok {
		t.Fatal("missing aiscan ext namespace")
	}
	extMap, ok := ext.(map[string]any)
	if !ok {
		t.Fatalf("ext.aiscan is not map: %T", ext)
	}
	if extMap["eval_round"] != 2 {
		t.Errorf("eval_round = %v, want 2", extMap["eval_round"])
	}
	if extMap["eval_pass"] != true {
		t.Errorf("eval_pass = %v, want true", extMap["eval_pass"])
	}
}
