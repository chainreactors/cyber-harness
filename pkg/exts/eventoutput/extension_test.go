package eventoutput_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/output"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
)

func TestOutputIsInertThenDrainsCanonicalEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	events := coreevents.New()
	writer, err := eventoutput.New(events, eventoutput.Options{Path: path, Queue: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor touched output: %v", err)
	}
	set, err := extension.New(extension.Entry{ID: "output", Extension: writer})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		events.Publish(&aop.Event{Id: string(rune('a' + i)), Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	events.Publish(&aop.Event{Id: "late", Payload: &aop.Event_Status{Status: &aop.Status{State: "late"}}})
	recorded, err := output.ReadJSONL(path)
	if err != nil || len(recorded) != 4 {
		t.Fatalf("events=%d err=%v", len(recorded), err)
	}
	for i, event := range recorded {
		if event.GetId() != string(rune('a'+i)) || event.GetSessionId() != "" {
			t.Fatalf("event %d = %v", i, event)
		}
	}
}

func TestOutputRejectsExistingDestinationWithoutTruncating(t *testing.T) {
	for _, want := range [][]byte{nil, []byte("existing\n")} {
		path := filepath.Join(t.TempDir(), "existing.jsonl")
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
		writer, err := eventoutput.New(coreevents.New(), eventoutput.Options{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		set, err := extension.New(extension.Entry{ID: "output", Extension: writer})
		if err != nil {
			t.Fatal(err)
		}
		if err := set.Load(t.Context()); err == nil {
			t.Fatal("loaded an existing output destination")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("existing output was changed: %q", got)
		}
	}
}

func TestOutputFlushMakesAdmittedEventsVisibleAndKeepsAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	events := coreevents.New()
	writer, err := eventoutput.New(events, eventoutput.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "output", Extension: writer})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	events.Publish(&aop.Event{Id: "first", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	if err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	recorded, err := output.ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].GetId() != "first" {
		t.Fatalf("flushed output: %v", recorded)
	}
	events.Publish(&aop.Event{Id: "second", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorded, err = output.ReadJSONL(path)
	if err != nil || len(recorded) != 2 || recorded[1].GetId() != "second" {
		t.Fatalf("closed output: %v %v", recorded, err)
	}
}
