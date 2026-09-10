package plugin_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/plugin"
)

type testModule struct {
	name     string
	events   *[]string
	loadErr  error
	closeErr error
	started  chan struct{}
	release  chan struct{}
}

func (m *testModule) Load(ctx context.Context) error {
	*m.events = append(*m.events, "load:"+m.name)
	if m.started != nil {
		close(m.started)
		select {
		case <-m.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.loadErr
}

func (m *testModule) Close(context.Context) error {
	*m.events = append(*m.events, "close:"+m.name)
	return m.closeErr
}

func entry(m *testModule, deps ...capability.ID) plugin.Entry {
	return plugin.Entry{Descriptor: capability.Descriptor{ID: capability.ID(m.name), DependsOn: deps}, Module: m}
}

func newSet(t *testing.T, entries ...plugin.Entry) *plugin.Set {
	t.Helper()
	s, err := plugin.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertEvents(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestDependencyOrderAndIdempotence(t *testing.T) {
	var events []string
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events}
	c := &testModule{name: "c", events: &events}
	s := newSet(t, entry(b, "a"), entry(c), entry(a))
	if err := s.Load(t.Context(), "b", "c"); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Unload(t.Context(), "a"); err == nil {
		t.Fatal("accepted live dependent")
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c"})
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c", "close:c", "close:b", "close:a"})
	if err := s.Load(t.Context(), "a"); err == nil {
		t.Fatal("reopened closed set")
	}
}

func TestValidationBeforeSideEffects(t *testing.T) {
	var events []string
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events}
	for name, entries := range map[string][]plugin.Entry{
		"empty id":   {{Module: a}},
		"nil module": {{Descriptor: capability.Descriptor{ID: "a"}}},
		"duplicate":  {entry(a), entry(b), entry(a)},
		"missing":    {entry(a, "missing")},
		"self cycle": {entry(a, "a")},
		"cycle":      {entry(a, "b"), entry(b, "a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := plugin.New(entries...); err == nil {
				t.Fatal("accepted invalid graph")
			}
		})
	}
	s := newSet(t, entry(a))
	if err := s.Load(t.Context(), "a", "missing"); err == nil {
		t.Fatal("accepted unknown selection")
	}
	if err := s.Unload(t.Context(), "missing"); err == nil {
		t.Fatal("accepted unknown unload")
	}
	assertEvents(t, events, nil)
}

func TestExplicitSelectionAndCopiedDependencies(t *testing.T) {
	var events []string
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events}
	c := &testModule{name: "c", events: &events}
	deps := []capability.ID{"a"}
	s := newSet(t, entry(a), entry(b, deps...), entry(c))
	deps[0] = "missing"
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, nil)
	if err := s.Load(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "close:b", "close:a"})
}

func TestRollbackClosesPartialLoadAndPreservesActiveModules(t *testing.T) {
	var events []string
	loadErr := errors.New("partial initialization")
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events}
	c := &testModule{name: "c", events: &events, loadErr: loadErr}
	s := newSet(t, entry(a), entry(b, "a"), entry(c, "b"))
	if err := s.Load(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(t.Context(), "c"); !errors.Is(err, loadErr) {
		t.Fatalf("load error: %v", err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c", "close:c", "close:b"})
	if err := s.Load(t.Context(), "b"); err == nil {
		t.Fatal("reloaded closed instance")
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c", "close:c", "close:b", "close:a"})
}

func TestRollbackFailureRetainsDependenciesAndBothErrors(t *testing.T) {
	var events []string
	loadErr, closeErr := errors.New("load"), errors.New("close")
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events, loadErr: loadErr, closeErr: closeErr}
	s := newSet(t, entry(a), entry(b, "a"))
	err := s.Load(t.Context(), "b")
	if !errors.Is(err, loadErr) || !errors.Is(err, closeErr) {
		t.Fatalf("lost errors: %v", err)
	}
	if err := s.Unload(t.Context(), "a"); err == nil {
		t.Fatal("released dependency of stopping module")
	}
	if err := s.Load(t.Context(), "b"); err == nil {
		t.Fatal("reloaded stopping module")
	}
	assertEvents(t, events, []string{"load:a", "load:b", "close:b"})
	b.closeErr = nil
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "close:b", "close:b", "close:a"})
}

func TestCloseRetriesTimeoutAndClosesUnrelatedModules(t *testing.T) {
	var events []string
	a := &testModule{name: "a", events: &events}
	b := &testModule{name: "b", events: &events, closeErr: context.DeadlineExceeded}
	c := &testModule{name: "c", events: &events}
	s := newSet(t, entry(c), entry(a), entry(b, "a"))
	if err := s.Load(t.Context(), "b", "c"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error: %v", err)
	}
	assertEvents(t, events, []string{"load:c", "load:a", "load:b", "close:b", "close:c"})
	b.closeErr = nil
	if err := s.Unload(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:c", "load:a", "load:b", "close:b", "close:c", "close:b", "close:a"})
}

func TestConcurrentTransactionsAndCanceledWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var events []string
		m := &testModule{name: "m", events: &events, started: make(chan struct{})}
		var release func()
		m.release, release = barrier(t)
		s := newSet(t, entry(m))
		loaded := make(chan error, 1)
		go func() { loaded <- s.Load(t.Context(), "m") }()
		<-m.started
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		waiting := make(chan error, 1)
		go func() { waiting <- s.Load(ctx, "m") }()
		synctest.Wait()
		cancel()
		if err := <-waiting; !errors.Is(err, context.Canceled) {
			t.Fatalf("wait error: %v", err)
		}
		closed := make(chan error, 1)
		go func() { closed <- s.Close(t.Context()) }()
		// The blocked transaction is released before cleanup runs below.
		// Cleanup uses the same release function so failure cannot leak a waiter.
		release()
		if err := <-loaded; err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		assertEvents(t, events, []string{"load:m", "close:m"})
	})
}

func TestCanceledLoadRollsBackPartialInitialization(t *testing.T) {
	var events []string
	m := &testModule{name: "m", events: &events, started: make(chan struct{}), release: make(chan struct{})}
	s := newSet(t, entry(m))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Load(ctx, "m") }()
	<-m.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("load error: %v", err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:m", "close:m"})
}

func TestEmptySetAndIndependentInstances(t *testing.T) {
	empty := newSet(t)
	if err := empty.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	var first, second []string
	a := newSet(t, entry(&testModule{name: "same", events: &first}))
	b := newSet(t, entry(&testModule{name: "same", events: &second}))
	if err := a.Load(t.Context(), "same"); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(t.Context(), "same"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, first, []string{"load:same", "close:same"})
	assertEvents(t, second, []string{"load:same"})
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
