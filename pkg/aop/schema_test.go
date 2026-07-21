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

	var (
		seenReasoningDelta = false
		seenComplete       = false
		seenStatusExt      = false
	)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		if !event.Valid() {
			t.Fatalf("invalid fixture envelope: %+v", event)
		}
		switch event.Type {
		case TypeMessage:
			var data MessageData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.MessageID == "" || data.Role == "" || len(data.Parts) == 0 {
				t.Fatalf("invalid message payload: %+v", data)
			}
			seenComplete = true
		case TypeMessageDelta:
			var data MessageDeltaData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.MessageID == "" || data.PartType == "" {
				t.Fatalf("invalid message.delta payload: %+v", data)
			}
			seenReasoningDelta = seenReasoningDelta || data.PartType == PartReasoning
		case TypeStatus:
			if len(event.Ext) > 0 {
				seenStatusExt = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenReasoningDelta {
		t.Fatal("canonical fixtures do not cover reasoning deltas")
	}
	if !seenComplete {
		t.Fatal("canonical fixtures do not cover complete messages")
	}
	if !seenStatusExt {
		t.Fatal("canonical fixtures do not cover status ext payloads")
	}
}
