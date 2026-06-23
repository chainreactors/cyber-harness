package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SessionData struct {
	Version   int           `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Model     string        `json:"model,omitempty"`
	Provider  string        `json:"provider,omitempty"`
	Messages  []ChatMessage `json:"messages"`
}

const sessionVersion = 1

func SaveSession(dir string, data *SessionData) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	now := time.Now()
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	data.UpdatedAt = now
	data.Version = sessionVersion
	data.Messages = sanitizeMessagesForSave(data.Messages)

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	ts := now.Format("20060102-150405")
	tsPath := filepath.Join(dir, fmt.Sprintf("session-%s.json", ts))
	if err := os.WriteFile(tsPath, raw, 0o644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	latestPath := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(latestPath, raw, 0o644); err != nil {
		return fmt.Errorf("write latest session: %w", err)
	}
	return nil
}

func LoadSession(path string) (*SessionData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	var data SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	return &data, nil
}

func LatestSessionPath(dir string) string {
	return filepath.Join(dir, "latest.json")
}

func sanitizeMessagesForSave(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(messages))
	for i, m := range messages {
		if len(m.ContentParts) > 0 {
			var text strings.Builder
			for _, p := range m.ContentParts {
				if p.Type == "text" {
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(p.Text)
				}
			}
			content := text.String()
			out[i] = ChatMessage{
				Role:       m.Role,
				Content:    &content,
				ToolCalls:  m.ToolCalls,
				ToolCallID: m.ToolCallID,
			}
		} else {
			out[i] = ChatMessage{
				Role:       m.Role,
				Content:    m.Content,
				ToolCalls:  m.ToolCalls,
				ToolCallID: m.ToolCallID,
			}
		}
	}
	return out
}
