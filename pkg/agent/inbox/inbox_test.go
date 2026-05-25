package inbox

import (
	"sync"
	"testing"
)

func TestBufferedPushDrain(t *testing.T) {
	b := NewBuffered(4)
	if !b.Push(NewUserMessage("a")) {
		t.Fatal("push to empty buffer should succeed")
	}
	if !b.Push(NewUserMessage("b")) {
		t.Fatal("push should succeed")
	}
	msgs := b.Drain()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if *msgs[0].ChatMessage.Content != "a" {
		t.Errorf("expected 'a', got %q", *msgs[0].ChatMessage.Content)
	}
	if *msgs[1].ChatMessage.Content != "b" {
		t.Errorf("expected 'b', got %q", *msgs[1].ChatMessage.Content)
	}
	if b.Drain() != nil {
		t.Error("drain on empty buffer should return nil")
	}
}

func TestBufferedCapacity(t *testing.T) {
	b := NewBuffered(2)
	b.Push(NewUserMessage("a"))
	b.Push(NewUserMessage("b"))
	if b.Push(NewUserMessage("c")) {
		t.Fatal("push beyond capacity should fail")
	}
}

func TestBufferedClose(t *testing.T) {
	b := NewBuffered(4)
	b.Push(NewUserMessage("a"))
	b.Close()
	if !b.Closed() {
		t.Fatal("should be closed")
	}
	if b.Push(NewUserMessage("b")) {
		t.Fatal("push to closed buffer should fail")
	}
	msgs := b.Drain()
	if len(msgs) != 1 {
		t.Fatal("drain after close should return buffered messages")
	}
}

func TestBufferedPriorityOrdering(t *testing.T) {
	b := NewBuffered(8)
	b.Push(NewUserMessage("normal-1"))
	b.Push(NewUserMessage("low").WithPriority(PriorityLow))
	b.Push(NewUserMessage("high").WithPriority(PriorityHigh))
	b.Push(NewUserMessage("normal-2"))

	msgs := b.Drain()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	expected := []string{"high", "normal-1", "normal-2", "low"}
	for i, want := range expected {
		got := *msgs[i].ChatMessage.Content
		if got != want {
			t.Errorf("position %d: expected %q, got %q", i, want, got)
		}
	}
}

func TestBufferedStableOrderWithinPriority(t *testing.T) {
	b := NewBuffered(8)
	b.Push(NewUserMessage("a").WithPriority(PriorityHigh))
	b.Push(NewUserMessage("b").WithPriority(PriorityHigh))
	b.Push(NewUserMessage("c").WithPriority(PriorityHigh))

	msgs := b.Drain()
	for i, want := range []string{"a", "b", "c"} {
		got := *msgs[i].ChatMessage.Content
		if got != want {
			t.Errorf("position %d: expected %q, got %q", i, want, got)
		}
	}
}

func TestBufferedConcurrency(t *testing.T) {
	b := NewBuffered(1000)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Push(NewUserMessage("msg"))
			}
		}()
	}
	wg.Wait()

	var total int
	for {
		msgs := b.Drain()
		if msgs == nil {
			break
		}
		total += len(msgs)
	}
	if total != 1000 {
		t.Fatalf("expected 1000 messages, got %d", total)
	}
}

func TestNewBufferedMinCapacity(t *testing.T) {
	b := NewBuffered(0)
	if !b.Push(NewUserMessage("a")) {
		t.Fatal("capacity 0 should be clamped to 1")
	}
	if b.Push(NewUserMessage("b")) {
		t.Fatal("should fail at capacity 1")
	}
}
