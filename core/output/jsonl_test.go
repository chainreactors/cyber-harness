package output

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
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

func jsonlTestMessage(id string) *aop.Event {
	return &aop.Event{
		Id: id, SessionId: "session", Emitter: "test",
		Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(id)}}},
	}
}
