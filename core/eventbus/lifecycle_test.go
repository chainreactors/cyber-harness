package eventbus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestCloseRejectsOldSnapshot(t *testing.T) {
	bus := New[int]()
	var next *Subscription[int]
	bus.Subscribe(func(int) {
		// Emit already took its snapshot, but has not admitted next yet.
		if err := next.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	next = bus.Subscribe(func(int) { t.Error("closed callback ran from old snapshot") })
	bus.Emit(1)
	bus.Emit(2)
}

func TestSynchronousCloseTimeoutRetainsCallbacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := New[int]()
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		defer close(release)
		s := bus.Subscribe(func(int) { entered <- struct{}{}; <-release })
		go bus.Emit(1)
		go bus.Emit(2)
		<-entered
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v", err)
		}
		select {
		case <-s.Done():
			t.Fatal("Close completed with callbacks still running")
		default:
		}
		select {
		case <-s.Stopped():
		default:
			t.Fatal("Close did not stop admission")
		}
		bus.Emit(3) // Must not enter a third blocked handler.
		release <- struct{}{}
		release <- struct{}{}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatalf("completed Close with expired context = %v", err)
		}
	})
}

func TestSynchronousCallbackCanCancelItself(t *testing.T) {
	bus := New[int]()
	var s *Subscription[int]
	calls := 0
	s = bus.Subscribe(func(int) {
		calls++
		s.Cancel()
		select {
		case <-s.Done():
			t.Error("Done closed inside executing callback")
		default:
		}
	})
	bus.Emit(1)
	bus.Emit(2)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestSynchronousPanicReleasesAdmission(t *testing.T) {
	bus := New[int]()
	s := bus.Subscribe(func(int) { panic("handler failed") })
	func() {
		defer func() {
			if got := recover(); got != "handler failed" {
				t.Errorf("panic = %v", got)
			}
		}()
		bus.Emit(1)
	}()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSynchronousConcurrentCloseAndEmit(t *testing.T) {
	bus := New[int]()
	var calls atomic.Int64
	s := bus.Subscribe(func(int) { calls.Add(1) })
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				bus.Emit(1)
			}
		})
		wg.Go(func() {
			s.Cancel()
			if err := s.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	before := calls.Load()
	bus.Emit(1)
	if calls.Load() != before {
		t.Fatal("callback ran after Close")
	}
}

func TestFilteredCloseIncludesFilter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := New[int]()
		entered, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		s := bus.SubscribeFiltered(func(int) bool { close(entered); <-release; return false }, func(int) { t.Error("filtered callback ran") })
		go bus.Emit(1)
		<-entered
		s.Cancel()
		select {
		case <-s.Done():
			t.Fatal("filter is still executing")
		default:
		}
		release <- struct{}{}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCancelDiscardsQueueButWaitsForCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := New[int]()
		entered, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		var values []int
		var dropped uint64
		s, err := bus.SubscribeAsync(SubscribeOptions[int]{Buffer: 4, OnDrop: func(n uint64) { dropped = n }}, func(value int) error {
			values = append(values, value)
			close(entered)
			<-release
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		bus.Emit(1)
		<-entered
		bus.Emit(2)
		s.Cancel()
		select {
		case <-s.Done():
			t.Fatal("Cancel completed an executing callback")
		default:
		}
		release <- struct{}{}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(values) != 1 || values[0] != 1 || dropped != 1 {
			t.Fatalf("values=%v dropped=%d", values, dropped)
		}
	})
}
