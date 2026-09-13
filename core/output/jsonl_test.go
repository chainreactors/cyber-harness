package output

import (
	"os"
	"path/filepath"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestReadJSONLReadsCanonicalEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event := jsonlTestMessage("event-1")
	line, err := protojson.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Id != event.Id || events[0].SessionId != event.SessionId {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanJSONLRejectsNonEventLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	if err := os.WriteFile(path, []byte("traffic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJSONL(path); err == nil {
		t.Fatal("ReadJSONL accepted a non-event line")
	}
}

func TestScanJSONLRejectsEventsWithoutIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	line, err := protojson.Marshal(&aop.Event{SessionId: "session", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJSONL(path); err == nil {
		t.Fatal("ReadJSONL accepted an event without an id")
	}
}

func jsonlTestMessage(id string) *aop.Event {
	return &aop.Event{
		Id: id, SessionId: "session", Emitter: "test",
		Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(id)}}},
	}
}
