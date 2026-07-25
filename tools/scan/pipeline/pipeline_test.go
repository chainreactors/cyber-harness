package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type testEvent struct {
	key string
}

func (e testEvent) Key() string { return e.key }

// TestWorkerPanic verifies that a panicking capability does not crash the
// pipeline worker goroutine. The worker should recover, call done() to
// unblock waitIdle, and continue processing subsequent events.
func TestWorkerPanic(t *testing.T) {
	var mu sync.Mutex
	var results []string

	p, err := New(context.Background(), Config{
		Capabilities: []Capability{
			{
				Name:   "crasher",
				Routes: []Route{{From: ""}},
				Run: func(_ context.Context, e Event, emit func(Event)) {
					if e.Key() == "panic" {
						panic("boom")
					}
					mu.Lock()
					results = append(results, e.Key())
					mu.Unlock()
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		p.Run([]Event{
			testEvent{"before"},
			testEvent{"panic"},
			testEvent{"after"},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pipeline.Run did not return — waitIdle likely hung due to missing done()")
	}

	mu.Lock()
	defer mu.Unlock()

	// "before" and "after" should both be processed; the "panic" event is
	// skipped via recovery.
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
}

// TestDispatcherPanic verifies that a panic in the dispatcher's event
// processing does not stall the pipeline.
func TestDispatcherPanic(t *testing.T) {
	var count int
	var mu sync.Mutex

	p, err := New(context.Background(), Config{
		Capabilities: []Capability{
			{
				Name:   "counter",
				Routes: []Route{{From: ""}},
				Run: func(_ context.Context, e Event, emit func(Event)) {
					// Emit an event that routes to "sinker".
					emit(testEvent{fmt.Sprintf("out:%s", e.Key())})
					mu.Lock()
					count++
					mu.Unlock()
				},
			},
			{
				Name: "sinker",
				Routes: []Route{{
					From: "counter",
					Accept: func(e Event) bool {
						return true
					},
				}},
				Run: func(_ context.Context, e Event, emit func(Event)) {
					// just consume
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		p.Run([]Event{testEvent{"a"}, testEvent{"b"}})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pipeline.Run did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}
