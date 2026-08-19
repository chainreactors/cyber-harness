package agent

import (
	"context"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
)

func TestLoopSchedulerProducerLifecycle(t *testing.T) {
	ib := inbox.NewBuffered(1)
	scheduler := NewLoopScheduler(context.Background(), ib, nil)

	name, err := scheduler.Add(LoopEntry{
		Name:     "test-loop",
		Prompt:   "check progress",
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got := ib.ActiveProducers(); got != 1 {
		t.Fatalf("active producers after Add() = %d, want 1", got)
	}

	if err := scheduler.Remove(name); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for ib.ActiveProducers() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := ib.ActiveProducers(); got != 0 {
		t.Fatalf("active producers after Remove() = %d, want 0", got)
	}
}
