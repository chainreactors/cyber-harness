package aop

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalCyberUIFixtures(t *testing.T) {
	path := filepath.Join("..", "..", "web", "frontend", "cyber-ui", "packages", "agent-protocol", "fixtures", "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	seenReasoning := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		if !event.Valid() {
			t.Fatalf("invalid fixture envelope: %+v", event)
		}
		if event.Type == TypeText {
			var data TextData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			seenReasoning = seenReasoning || data.Channel == TextChannelReasoning
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenReasoning {
		t.Fatal("canonical fixtures do not cover reasoning text")
	}
}
