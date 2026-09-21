package inbox

import (
	"errors"
	"sync"
	"testing"
)

func TestInterruptAdmissionAndDrain(t *testing.T) {
	b := NewBuffered(2)
	signal := b.InterruptSignal()
	if err := b.Push(NewUserMessage("ordinary")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
		t.Fatal("ordinary input interrupted")
	default:
	}
	m := NewUserMessage("change course")
	m.Interrupt = true
	if err := b.Push(m); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
	default:
		t.Fatal("interrupt did not notify existing observer")
	}
	select {
	case <-b.InterruptSignal():
	default:
		t.Fatal("late observer missed interrupt")
	}
	if got := b.Drain(); len(got) != 2 || !got[1].Interrupt {
		t.Fatalf("messages = %#v", got)
	}
	select {
	case <-b.InterruptSignal():
		t.Fatal("consumed interrupt remained pending")
	default:
	}
	if err := b.Push(NewUserMessage("full-1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Push(NewUserMessage("full-2")); err != nil {
		t.Fatal(err)
	}
	if err := b.Push(m); !errors.Is(err, ErrInboxFull) {
		t.Fatal(err)
	}
	select {
	case <-b.InterruptSignal():
		t.Fatal("rejected message interrupted")
	default:
	}
	b.Close()
	if err := b.Push(m); !errors.Is(err, ErrInboxClosed) {
		t.Fatal(err)
	}
}

func TestInterruptConcurrentDeliveryAndDrain(t *testing.T) {
	for i := 0; i < 200; i++ {
		b := NewBuffered(2)
		m := NewUserMessage("incoming")
		m.Interrupt = true
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); _ = b.Push(m) }()
		consumed := b.Drain()
		signal := b.InterruptSignal()
		wg.Wait()
		if len(consumed) == 0 {
			select {
			case <-signal:
			default:
				t.Fatal("unconsumed interrupt lost across Drain/observe")
			}
			if len(b.Drain()) != 1 {
				t.Fatal("message lost")
			}
		}
		b.Close()
	}
}
