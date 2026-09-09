package eventbus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitSubscription(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not finish")
	}
}

func TestSelectiveSubscriptionOwnsDataAndDrains(t *testing.T) {
	b := New[[]byte]()
	gate := make(chan struct{})
	var got []string
	s, err := b.SubscribeAsync(SubscribeOptions[[]byte]{
		Buffer: 8, Filter: func(v []byte) bool { return len(v) > 0 },
		Clone: func(v []byte) []byte { return append([]byte(nil), v...) },
	}, func(v []byte) error { <-gate; got = append(got, string(v)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	b.Emit(nil)
	v := []byte("first")
	b.Emit(v)
	v[0] = 'X'
	b.Emit([]byte("second"))
	close(gate)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.Emit([]byte("ignored"))
	if strings.Join(got, ",") != "first,second" {
		t.Fatalf("events = %v", got)
	}
}

func TestSlowSubscriptionIsolatedAndBudgetIncludesHandler(t *testing.T) {
	for _, byBytes := range []bool{false, true} {
		t.Run(map[bool]string{false: "count", true: "bytes"}[byBytes], func(t *testing.T) {
			b := New[string]()
			entered := make(chan struct{})
			gate := make(chan struct{})
			var got, drops int
			b.Subscribe(func(string) { got++ })
			opts := SubscribeOptions[string]{Buffer: 2, OnDrop: func(n uint64) { drops = int(n) }}
			if byBytes {
				opts.Buffer = 8
				opts.MaxBytes = 4
				opts.Size = func(v string) int64 { return int64(len(v)) }
			}
			s, err := b.SubscribeAsync(opts, func(string) error { close(entered); <-gate; return nil })
			if err != nil {
				t.Fatal(err)
			}
			b.Emit("aa")
			waitSubscription(t, entered)
			b.Emit("bb")
			b.Emit("cc")
			waitSubscription(t, s.Stopped())
			if !errors.Is(s.Err(), ErrOverflow) {
				t.Fatalf("error = %v", s.Err())
			}
			b.Emit("dd")
			close(gate)
			waitSubscription(t, s.Done())
			if got != 4 || drops != 2 {
				t.Fatalf("healthy=%d drops=%d", got, drops)
			}
		})
	}
}

func TestSubscriptionPanicStopsOnlyThatSubscriber(t *testing.T) {
	b := New[int]()
	reported := make(chan error, 1)
	s, err := b.SubscribeAsync(SubscribeOptions[int]{OnError: func(err error) { reported <- err }}, func(int) error { panic("broken") })
	if err != nil {
		t.Fatal(err)
	}
	b.Emit(1)
	waitSubscription(t, s.Done())
	if err := <-reported; !strings.Contains(err.Error(), "broken") {
		t.Fatal(err)
	}
	s.Cancel()
	s.Cancel()
}

func TestSubscriptionConcurrentCancelAndEmit(t *testing.T) {
	b := New[int]()
	for i := 0; i < 32; i++ {
		s, err := b.SubscribeAsync(SubscribeOptions[int]{Buffer: 64}, func(int) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < 32; k++ {
					b.Emit(k)
				}
			}()
		}
		s.Cancel()
		wg.Wait()
		waitSubscription(t, s.Done())
	}
}

func TestEmitWithoutSubscribersDoesNotAllocate(t *testing.T) {
	b := New[int]()
	if n := testing.AllocsPerRun(100, func() { b.Emit(1) }); n != 0 {
		t.Fatalf("allocs=%v", n)
	}
}
