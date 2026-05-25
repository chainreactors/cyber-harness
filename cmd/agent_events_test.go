package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

func TestEventsWriterAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsWriter(path)
	if err != nil {
		t.Fatalf("newEventsWriter() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

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
			Message: provider.ChatMessage{
				Role:    "assistant",
				Content: &content,
			},
		},
		{Type: agent.EventAgentEnd, Turn: 1, NewMessages: make([]provider.ChatMessage, 3)},
	}
	for _, e := range events {
		if err := w.HandleEvent(context.Background(), e); err != nil {
			t.Fatalf("HandleEvent() error = %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open events file: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var lines []map[string]any
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events file: %v", err)
	}
	if got, want := len(lines), len(events); got != want {
		t.Fatalf("line count = %d, want %d", got, want)
	}

	// Spot-check a few events.
	if lines[0]["type"] != string(agent.EventAgentStart) {
		t.Errorf("line[0].type = %v, want %s", lines[0]["type"], agent.EventAgentStart)
	}
	if _, ok := lines[0]["ts"].(string); !ok {
		t.Errorf("line[0] missing ts field")
	}
	if lines[2]["tool_name"] != "bash" {
		t.Errorf("line[2].tool_name = %v, want bash", lines[2]["tool_name"])
	}
	// agent_end records the new-message count, not the messages themselves.
	if v, _ := lines[5]["new_messages"].(float64); v != 3 {
		t.Errorf("line[5].new_messages = %v, want 3", lines[5]["new_messages"])
	}
}

func TestEventsWriterTruncatesLargeFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := newEventsWriter(path)
	if err != nil {
		t.Fatalf("newEventsWriter() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	huge := strings.Repeat("a", eventResultLimit+1024)
	if err := w.HandleEvent(context.Background(), agent.Event{
		Type:   agent.EventToolExecutionEnd,
		Result: huge,
	}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	_ = w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "[truncated") {
		t.Fatalf("expected truncation marker in: %s", string(data))
	}
	if len(data) > eventResultLimit+2048 {
		t.Fatalf("written line should be bounded; got %d bytes", len(data))
	}
}

func TestEventsWriterNilWhenPathEmpty(t *testing.T) {
	w, err := newEventsWriter("")
	if err != nil {
		t.Fatalf("newEventsWriter() error = %v", err)
	}
	if w != nil {
		t.Fatalf("expected nil writer for empty path, got %#v", w)
	}
	// Both Close and HandleEvent must be safe on a nil writer.
	if err := w.Close(); err != nil {
		t.Fatalf("Close() on nil writer error = %v", err)
	}
	if err := w.HandleEvent(context.Background(), agent.Event{Type: agent.EventAgentStart}); err != nil {
		t.Fatalf("HandleEvent() on nil writer error = %v", err)
	}
}

func TestCombineEventHandlersRunsAllAndReportsFirstError(t *testing.T) {
	var calls []string
	a := func(_ context.Context, _ agent.Event) error {
		calls = append(calls, "a")
		return errors.New("a-failed")
	}
	b := func(_ context.Context, _ agent.Event) error {
		calls = append(calls, "b")
		return nil
	}
	c := func(_ context.Context, _ agent.Event) error {
		calls = append(calls, "c")
		return errors.New("c-failed")
	}
	handler := combineEventHandlers(nil, a, b, c)
	err := handler(context.Background(), agent.Event{Type: agent.EventAgentStart})
	if err == nil || err.Error() != "a-failed" {
		t.Fatalf("err = %v, want a-failed", err)
	}
	if got, want := strings.Join(calls, ","), "a,b,c"; got != want {
		t.Fatalf("call order = %s, want %s", got, want)
	}
}

func TestCombineEventHandlersNilWhenAllNil(t *testing.T) {
	if h := combineEventHandlers(nil, nil); h != nil {
		t.Fatalf("expected nil handler when all inputs nil, got %#v", h)
	}
}
