//go:build live_llm

package harness_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func uniqueIOAMessage(t *testing.T, messages []ioaMessage, text string) ioaMessage {
	t.Helper()
	var found []ioaMessage
	for _, m := range messages {
		if m.Content.Text == text {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected one %q message, got %d in %+v", text, len(found), messages)
	}
	if found[0].ID == "" || found[0].Sender == "" || found[0].SpaceID == "" {
		t.Fatalf("missing IOA identity: %+v", found[0])
	}
	return found[0]
}

func readAudit(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func (p *stdioClient) eventSnapshot() []map[string]any {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	return append([]map[string]any(nil), p.events...)
}
