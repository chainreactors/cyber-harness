package output

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"google.golang.org/protobuf/proto"
)

func TestJSONLRecorderWritesConcurrentEventsAsCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	bus := eventbus.New[*aop.Event]()
	recorder, err := NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 64
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			bus.Emit(&aop.Event{
				Id: fmt.Sprintf("event-%d", index), SessionId: "session", Emitter: "test",
				Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(fmt.Sprint(index))}}},
			})
		}(i)
	}
	wg.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count {
		t.Fatalf("events = %d, want %d", len(events), count)
	}
}

func TestJSONLRecorderSwitchesFilesWithoutReplayingHistory(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.jsonl")
	second := filepath.Join(dir, "second.jsonl")
	bus := eventbus.New[*aop.Event]()
	recorder, err := NewJSONLRecorder(bus, first)
	if err != nil {
		t.Fatal(err)
	}
	bus.Emit(jsonlTestMessage("first"))
	if err := recorder.Switch(second); err != nil {
		t.Fatal(err)
	}
	bus.Emit(jsonlTestMessage("second"))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	firstEvents, err := ReadJSONL(first)
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := ReadJSONL(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstEvents) != 1 || firstEvents[0].Id != "first" {
		t.Fatalf("first events = %#v", firstEvents)
	}
	if len(secondEvents) != 1 || secondEvents[0].Id != "second" {
		t.Fatalf("second events = %#v", secondEvents)
	}
}

func TestJSONLRecorderSkipsDuplicateEventIDWithinSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	bus := eventbus.New[*aop.Event]()
	recorder, err := NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	event := &aop.Event{Id: "event-retry", SessionId: "session-1", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}}
	bus.Emit(event)
	bus.Emit(proto.Clone(event).(*aop.Event))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("JSONL events = %d, want 1", len(events))
	}
}

func TestJSONLRecorderLoadsExistingEventIDsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event := jsonlTestMessage("persisted")
	bus := eventbus.New[*aop.Event]()
	first, err := NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	bus.Emit(event)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	bus.Emit(proto.Clone(event).(*aop.Event))
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events after recorder restart = %d, want 1", len(events))
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

func TestJSONLRecorderRejectsEventsWithoutIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	recorder, err := NewJSONLRecorder(eventbus.New[*aop.Event](), path)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Write(&aop.Event{SessionId: "session", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}}); err == nil {
		t.Fatal("Write accepted an event without an id")
	}
}

func jsonlTestMessage(id string) *aop.Event {
	return &aop.Event{
		Id: id, SessionId: "session", Emitter: "test",
		Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(id)}}},
	}
}
