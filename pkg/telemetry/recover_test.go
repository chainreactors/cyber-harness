package telemetry

import (
	"sync"
	"testing"
	"time"
)

func TestSafeRun_RecoversPanic(t *testing.T) {
	// SafeRun should not propagate the panic.
	SafeRun("test", func() {
		panic("boom")
	})
	// If we reach here, recovery worked.
}

func TestSafeRun_NormalExecution(t *testing.T) {
	var called bool
	SafeRun("test", func() {
		called = true
	})
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestSafeGo_RecoversPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	done := make(chan struct{})
	SafeGo("test", func() {
		defer func() { close(done) }()
		defer wg.Done()
		panic("goroutine boom")
	})

	select {
	case <-done:
		// Goroutine exited cleanly after panic recovery.
	case <-time.After(5 * time.Second):
		t.Fatal("SafeGo goroutine did not return")
	}
}

func TestSafeGo_NormalExecution(t *testing.T) {
	ch := make(chan string, 1)
	SafeGo("test", func() {
		ch <- "ok"
	})

	select {
	case v := <-ch:
		if v != "ok" {
			t.Fatalf("expected 'ok', got %q", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SafeGo goroutine did not execute")
	}
}
