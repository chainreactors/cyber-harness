package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadSession(t *testing.T) {
	dir := t.TempDir()

	content := "hello world"
	toolArgs := `{"cmd":"ls"}`
	messages := []ChatMessage{
		{Role: "user", Content: &content},
		{
			Role:    "assistant",
			Content: &content,
			ToolCalls: []ToolCall{
				{ID: "tc1", Type: "function", Function: FunctionCall{Name: "bash", Arguments: toolArgs}},
			},
		},
		{Role: "tool", Content: &content, ToolCallID: "tc1"},
	}

	data := &SessionData{
		Model:    "gpt-4o",
		Provider: "openai",
		Messages: messages,
	}
	if err := SaveSession(dir, data); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !os.IsNotExist(err) {
		t.Fatalf("latest.json should not be written, err=%v", err)
	}

	sessions, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}

	loaded, err := LoadSession(sessions[0].Path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.Version != sessionVersion {
		t.Errorf("version = %d, want %d", loaded.Version, sessionVersion)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("model = %q, want %q", loaded.Model, "gpt-4o")
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "user" || *loaded.Messages[0].Content != "hello world" {
		t.Errorf("message[0] = %+v", loaded.Messages[0])
	}
	if len(loaded.Messages[1].ToolCalls) != 1 || loaded.Messages[1].ToolCalls[0].Function.Name != "bash" {
		t.Errorf("message[1] tool_calls = %+v", loaded.Messages[1].ToolCalls)
	}
	if loaded.Messages[2].ToolCallID != "tc1" {
		t.Errorf("message[2] tool_call_id = %q, want %q", loaded.Messages[2].ToolCallID, "tc1")
	}

	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		if matched, _ := filepath.Match("session-*.json", e.Name()); matched {
			found = true
		}
	}
	if !found {
		t.Error("timestamped session file not found")
	}
}

func TestListSessionsSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	oldTime := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	writeSessionFile(t, filepath.Join(dir, "session-old.json"), SessionData{
		Version:   sessionVersion,
		UpdatedAt: oldTime,
		Model:     "old",
		Messages:  []ChatMessage{NewTextMessage("user", "old")},
	})
	writeSessionFile(t, filepath.Join(dir, "session-new.json"), SessionData{
		Version:   sessionVersion,
		UpdatedAt: newTime,
		Model:     "new",
		Messages:  []ChatMessage{NewTextMessage("user", "new")},
	})
	writeSessionFile(t, filepath.Join(dir, "latest.json"), SessionData{
		Version:   sessionVersion,
		UpdatedAt: newTime.Add(time.Hour),
		Model:     "ignored",
		Messages:  []ChatMessage{NewTextMessage("user", "ignored")},
	})

	sessions, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(sessions))
	}
	if filepath.Base(sessions[0].Path) != "session-new.json" {
		t.Fatalf("first session = %s, want session-new.json", sessions[0].Path)
	}
	if sessions[0].Messages != 1 || sessions[0].Model != "new" {
		t.Fatalf("session metadata = %+v", sessions[0])
	}
}

func writeSessionFile(t *testing.T, path string, data SessionData) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func TestSanitizeMessagesForSave(t *testing.T) {
	text := "some text"
	reasoning := "thinking..."
	msgs := []ChatMessage{
		{
			Role:             "assistant",
			Content:          &text,
			ReasoningContent: &reasoning,
			ContentParts: []ContentPart{
				{Type: "text", Text: "part1"},
				{Type: "image_url"},
				{Type: "text", Text: "part2"},
			},
		},
	}
	out := sanitizeMessagesForSave(msgs)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Content == nil || *out[0].Content != "part1\npart2" {
		t.Errorf("content = %v, want %q", out[0].Content, "part1\npart2")
	}
	if len(out[0].ContentParts) != 0 {
		t.Error("ContentParts should be empty after sanitize")
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	_, err := LoadSession("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
