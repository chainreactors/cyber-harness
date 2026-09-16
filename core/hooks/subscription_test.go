package hooks

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestCancelRevokesOldSnapshot(t *testing.T) {
	r := New()
	p := NewPoint[int, struct{}]("test")
	var second *Subscription
	p.On(r, "first", func(context.Context, int) (struct{}, error) { second.Cancel(); return struct{}{}, nil })
	second = p.On(r, "second", func(context.Context, int) (struct{}, error) {
		t.Error("revoked callback started")
		return struct{}{}, nil
	})
	_, _ = p.Emit(t.Context(), r, 0)
	if err := second.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWaitsForAcceptedCallbackAndCanRetry(t *testing.T) {
	r := New()
	p := NewPoint[int, struct{}]("test")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sub := p.On(r, "worker", func(context.Context, int) (struct{}, error) { close(entered); <-release; return struct{}{}, nil })
	go func() { defer close(done); _, _ = p.Emit(context.Background(), r, 0) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sub.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close = %v", err)
	}
	_, _ = p.Emit(t.Context(), r, 0)
	close(release)
	<-done
	if err := sub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCancelAndClose(t *testing.T) {
	r := New()
	p := NewPoint[int, struct{}]("test")
	sub := p.On(r, "worker", func(context.Context, int) (struct{}, error) { return struct{}{}, nil })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = p.Emit(t.Context(), r, 0)
			sub.Cancel()
			if err := sub.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestNilRegistryRegistrationFails(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing registry silently accepted")
		}
	}()
	NewPoint[int, struct{}]("test").On(nil, "policy", func(context.Context, int) (struct{}, error) { return struct{}{}, nil })
}
