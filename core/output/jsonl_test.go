package output

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestJSONLRecorderStopsWritingAtSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capped.jsonl")
	bus := eventbus.New[*aop.Event]()
	recorder, err := NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	recorder.maxBytes = 512
	recorder.mu.Unlock()

	const emitted = 100
	for i := 0; i < emitted; i++ {
		bus.Emit(jsonlTestMessage(fmt.Sprintf("event-%d", i)))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The cap bounds the payload; only the one marker line may exceed it.
	if info.Size() > 512+256 {
		t.Fatalf("file size = %d, want bounded near 512", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "size limit reached") {
		t.Fatalf("truncation marker missing:\n%s", data)
	}
	// The marker is a non-JSON comment line: the file must stay readable.
	events, err := ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || len(events) >= emitted {
		t.Fatalf("persisted events = %d, want partial prefix of %d", len(events), emitted)
	}
	if err := recorder.Close(); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Close() error = %v, want size limit error", err)
	}
}

func TestJSONLRecorderSwitchResetsSizeBudget(t *testing.T) {
	dir := t.TempDir()
	bus := eventbus.New[*aop.Event]()
	recorder, err := NewJSONLRecorder(bus, filepath.Join(dir, "first.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	recorder.maxBytes = 256
	recorder.mu.Unlock()
	for i := 0; i < 10; i++ {
		bus.Emit(jsonlTestMessage(fmt.Sprintf("first-%d", i)))
	}
	second := filepath.Join(dir, "second.jsonl")
	if err := recorder.Switch(second); err != nil {
		t.Fatal(err)
	}
	bus.Emit(jsonlTestMessage("after-switch"))
	events, err := ReadJSONL(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Id != "after-switch" {
		t.Fatalf("second file events = %#v, want the post-switch event", events)
	}
}

func jsonlTestMessage(id string) *aop.Event {
	return &aop.Event{
		Id: id, SessionId: "session", Emitter: "test",
		Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(id)}}},
	}
}
