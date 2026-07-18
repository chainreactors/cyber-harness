package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

func parseAOPLines(t *testing.T, path string) []aop.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open events file: %v", err)
	}
	defer f.Close()

	var events []aop.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	for scanner.Scan() {
		var ev aop.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("invalid AOP line %q: %v", scanner.Text()[:80], err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events file: %v", err)
	}
	return events
}

func TestEventsFileSubscriberAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsFileSubscriber(path)
	if err != nil {
		t.Fatalf("newEventsFileSubscriber() error = %v", err)
	}
	defer w.Close()

	content := "spray returned no results"
	events := []agent.Event{
		{Type: agent.EventAgentStart},
		{Type: agent.EventTurnStart, Turn: 1},
		{
			Type:      agent.EventToolExecutionStart,
			Turn:      1,
			ToolName:  "bash",
			Arguments: `{"command":"spray -u http://x"}`,
		},
		{
			Type:    agent.EventToolExecutionEnd,
			Turn:    1,
			Result:  "ok",
			IsError: false,
		},
		{
			Type: agent.EventMessageEnd,
			Turn: 1,
			Message: agent.ChatMessage{
				Role:    "assistant",
				Content: &content,
			},
		},
		{Type: agent.EventAgentEnd, Turn: 1, Stop: agent.StopReasonCompleted, NewMessages: make([]agent.ChatMessage, 3)},
	}
	for _, e := range events {
		w.HandleEvent(e)
	}

	aopLines := parseAOPLines(t, path)
	// 6 agent events → AOP lines (session.start, turn.start, tool.call, tool.result, text, session.end)
	if len(aopLines) < 6 {
		t.Fatalf("expected at least 6 AOP lines, got %d", len(aopLines))
	}

	if aopLines[0].Type != aop.TypeSessionStart {
		t.Errorf("line[0].type = %s, want %s", aopLines[0].Type, aop.TypeSessionStart)
	}
	if aopLines[0].V != aop.Version {
		t.Errorf("line[0].v = %d, want %d", aopLines[0].V, aop.Version)
	}
	if aopLines[0].Agent != "aiscan" {
		t.Errorf("line[0].agent = %s, want aiscan", aopLines[0].Agent)
	}

	// tool.call should have tool_name=bash
	var toolCall aop.ToolCallData
	for _, ev := range aopLines {
		if ev.Type == aop.TypeToolCall {
			json.Unmarshal(ev.Data, &toolCall)
			break
		}
	}
	if toolCall.ToolName != "bash" {
		t.Errorf("tool.call.tool_name = %s, want bash", toolCall.ToolName)
	}

	// session.end should have stop=completed
	var sessionEnd aop.SessionEndData
	for _, ev := range aopLines {
		if ev.Type == aop.TypeSessionEnd {
			json.Unmarshal(ev.Data, &sessionEnd)
			break
		}
	}
	if sessionEnd.Stop != "completed" {
		t.Errorf("session.end.stop = %s, want completed", sessionEnd.Stop)
	}
}

func TestEventsFileSubscriberLargeFieldsPassThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsFileSubscriber(path)
	if err != nil {
		t.Fatalf("newEventsFileSubscriber() error = %v", err)
	}
	defer w.Close()

	huge := strings.Repeat("a", 20*1024)
	w.HandleEvent(agent.Event{
		Type:   agent.EventToolExecutionEnd,
		Result: huge,
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), huge) {
		t.Fatalf("expected full result in event log")
	}
}

func TestEventsFileSubscriberLLMRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsFileSubscriber(path)
	if err != nil {
		t.Fatalf("newEventsFileSubscriber() error = %v", err)
	}
	defer w.Close()

	w.HandleEvent(agent.Event{
		Type: agent.EventLLMRequest,
		Turn: 1,
		Request: &agent.ChatCompletionRequest{
			Model:    "deepseek-v4-pro",
			Messages: make([]agent.ChatMessage, 5),
			Tools:    make([]agent.ToolDefinition, 3),
		},
	})

	// llm_request is not mapped to any AOP event (internal-only)
	aopLines := parseAOPLines(t, path)
	if len(aopLines) != 0 {
		t.Errorf("llm_request should not produce AOP events, got %d", len(aopLines))
	}
}

func TestEventsFileSubscriberToolEndNoArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsFileSubscriber(path)
	if err != nil {
		t.Fatalf("newEventsFileSubscriber() error = %v", err)
	}
	defer w.Close()

	w.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		Turn:       1,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     "ok",
	})

	aopLines := parseAOPLines(t, path)
	if len(aopLines) != 1 {
		t.Fatalf("expected 1 AOP line, got %d", len(aopLines))
	}
	if aopLines[0].Type != aop.TypeToolResult {
		t.Errorf("type = %s, want %s", aopLines[0].Type, aop.TypeToolResult)
	}
	var data aop.ToolResultData
	json.Unmarshal(aopLines[0].Data, &data)
	if data.ToolName != "bash" {
		t.Errorf("tool_name = %s, want bash", data.ToolName)
	}
}

func TestEventsFileSubscriberErrorField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsFileSubscriber(path)
	if err != nil {
		t.Fatalf("newEventsFileSubscriber() error = %v", err)
	}
	defer w.Close()

	w.HandleEvent(agent.Event{
		Type:    agent.EventToolExecutionEnd,
		Turn:    1,
		IsError: true,
		Result:  "connection refused",
	})

	aopLines := parseAOPLines(t, path)
	if len(aopLines) != 1 {
		t.Fatalf("expected 1 AOP line, got %d", len(aopLines))
	}
	var data aop.ToolResultData
	json.Unmarshal(aopLines[0].Data, &data)
	if !data.IsError {
		t.Error("is_error should be true")
	}
	if data.Content != "connection refused" {
		t.Errorf("content = %v, want 'connection refused'", data.Content)
	}
}
