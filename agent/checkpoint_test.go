package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
)

func TestSaveAndLoadCheckpoint(t *testing.T) {
	dir := t.TempDir()

	content := "hello world"
	toolArgs := `{"cmd":"ls"}`
	messages := []*aop.Message{
		textMessage("user", content),
		{
			Role: "assistant",
			Content: []*aop.Content{
				aop.Text(content),
				toolCallContent("tc1", "bash", toolArgs),
			},
		},
		toolResultMessage("tc1", content),
	}

	data := &CheckpointData{
		Model:    "gpt-4o",
		Provider: "openai",
		Messages: messages,
	}
	if err := SaveCheckpoint(dir, data); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !os.IsNotExist(err) {
		t.Fatalf("latest.json should not be written, err=%v", err)
	}

	sessions, err := ListCheckpoints(dir)
	if err != nil {
		t.Fatalf("ListCheckpoints: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}

	loaded, err := LoadCheckpoint(sessions[0].Path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.Version != checkpointVersion {
		t.Errorf("version = %d, want %d", loaded.Version, checkpointVersion)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("model = %q, want %q", loaded.Model, "gpt-4o")
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "user" || provider.MessageText(loaded.Messages[0]) != "hello world" {
		t.Errorf("message[0] = %+v", loaded.Messages[0])
	}
	if calls := provider.MessageToolCalls(loaded.Messages[1]); len(calls) != 1 || calls[0].Name != "bash" {
		t.Errorf("message[1] tool_calls = %+v", calls)
	}
	if r := provider.MessageToolResult(loaded.Messages[2]); r == nil || r.CallId != "tc1" {
		t.Errorf("message[2] tool result = %+v, want call id tc1", r)
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

func TestListCheckpointsSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	oldTime := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	writeSessionFile(t, filepath.Join(dir, "session-old.json"), CheckpointData{
		Version:   checkpointVersion,
		UpdatedAt: oldTime,
		Model:     "old",
		Messages:  []*aop.Message{textMessage("user", "old")},
	})
	writeSessionFile(t, filepath.Join(dir, "session-new.json"), CheckpointData{
		Version:   checkpointVersion,
		UpdatedAt: newTime,
		Model:     "new",
		Messages:  []*aop.Message{textMessage("user", "new")},
	})
	writeSessionFile(t, filepath.Join(dir, "latest.json"), CheckpointData{
		Version:   checkpointVersion,
		UpdatedAt: newTime.Add(time.Hour),
		Model:     "ignored",
		Messages:  []*aop.Message{textMessage("user", "ignored")},
	})

	sessions, err := ListCheckpoints(dir)
	if err != nil {
		t.Fatalf("ListCheckpoints: %v", err)
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

func writeSessionFile(t *testing.T, path string, data CheckpointData) {
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
	msgs := []*aop.Message{
		{
			Role: "assistant",
			Content: []*aop.Content{
				aop.Reasoning("thinking..."),
				aop.Text("part1"),
				aop.Image("image/png", []byte("binary-image-data")),
				aop.Text("part2"),
			},
		},
	}
	out := sanitizeMessagesForSave(msgs)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if got := provider.MessageText(out[0]); got != "part1part2" {
		t.Errorf("content = %q, want %q", got, "part1part2")
	}
	for _, part := range out[0].Content {
		if part.GetMedia() != nil {
			t.Error("media parts should be stripped after sanitize")
		}
	}
	if got := provider.MessageReasoning(out[0]); got != "thinking..." {
		t.Errorf("reasoning = %q, want preserved", got)
	}
}

func TestLoadCheckpointNotFound(t *testing.T) {
	_, err := LoadCheckpoint("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
