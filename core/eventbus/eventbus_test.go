package eventbus

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestSubscribeAndEmit(t *testing.T) {
	bus := New[string]()
	var got []string
	bus.Subscribe(func(s string) { got = append(got, s) })
	bus.Emit("hello")
	bus.Emit("world")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("expected [hello world], got %v", got)
	}
}

func TestMultipleSubscribers(t *testing.T) {
	bus := New[int]()
	var a, b int
	bus.Subscribe(func(v int) { a += v })
	bus.Subscribe(func(v int) { b += v * 10 })
	bus.Emit(3)
	if a != 3 {
		t.Fatalf("a: expected 3, got %d", a)
	}
	if b != 30 {
		t.Fatalf("b: expected 30, got %d", b)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := New[int]()
	var count int
	unsub := bus.Subscribe(func(int) { count++ })
	bus.Emit(1)
	unsub.Cancel()
	bus.Emit(2)
	if count != 1 {
		t.Fatalf("expected 1 call after unsubscribe, got %d", count)
	}
}

func TestUnsubscribeMiddle(t *testing.T) {
	bus := New[int]()
	var a, b, c int
	bus.Subscribe(func(int) { a++ })
	unsub := bus.Subscribe(func(int) { b++ })
	bus.Subscribe(func(int) { c++ })
	bus.Emit(1)
	unsub.Cancel()
	bus.Emit(2)
	if a != 2 || b != 1 || c != 2 {
		t.Fatalf("expected a=2 b=1 c=2, got a=%d b=%d c=%d", a, b, c)
	}
}

func TestEmitNoSubscribers(t *testing.T) {
	bus := New[string]()
	bus.Emit("noop")
}

func TestConcurrentEmit(t *testing.T) {
	bus := New[int]()
	var total atomic.Int64
	bus.Subscribe(func(v int) { total.Add(int64(v)) })

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit(1)
		}()
	}
	wg.Wait()
	if total.Load() != 100 {
		t.Fatalf("expected 100, got %d", total.Load())
	}
}

func TestDoubleUnsubscribe(t *testing.T) {
	bus := New[int]()
	unsub := bus.Subscribe(func(int) {})
	unsub.Cancel()
	unsub.Cancel()
}
