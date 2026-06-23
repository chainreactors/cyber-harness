package agent

import (
	"os"
	"path/filepath"
	"testing"
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

	latestPath := LatestSessionPath(dir)
	if _, err := os.Stat(latestPath); err != nil {
		t.Fatalf("latest.json not found: %v", err)
	}

	loaded, err := LoadSession(latestPath)
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
