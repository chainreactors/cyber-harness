package extension_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
)

type testExtension struct {
	name     string
	events   *[]string
	loadErr  error
	closeErr error
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
}

func (m *testExtension) record(event string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.events = append(*m.events, event)
}

func (m *testExtension) Load(scope *extension.Context) error {
	ctx := scope.Init()
	m.record("load:" + m.name)
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

func (m *testExtension) Close(context.Context) error {
	m.record("close:" + m.name)
	return m.closeErr
}

func entry(m *testExtension, deps ...string) extension.Entry {
	return extension.Entry{ID: m.name, DependsOn: deps, Extension: m}
}

func newSet(t *testing.T, entries ...extension.Entry) *extension.Set {
	t.Helper()
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func assertEvents(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestFixedCompositionLoadsAndClosesInDependencyOrder(t *testing.T) {
	var events []string
	a := &testExtension{name: "a", events: &events}
	b := &testExtension{name: "b", events: &events}
	c := &testExtension{name: "c", events: &events}
	set := newSet(t, entry(b, "a"), entry(c), entry(a))
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c"})
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "load:c", "close:c", "close:b", "close:a"})
	if err := set.Load(t.Context()); err == nil {
		t.Fatal("closed composition loaded again")
	}
}

func TestGraphValidationPrecedesSideEffects(t *testing.T) {
	var events []string
	a := &testExtension{name: "a", events: &events}
	b := &testExtension{name: "b", events: &events}
	for name, entries := range map[string][]extension.Entry{
		"empty id":      {{Extension: a}},
		"nil extension": {{ID: "a"}},
		"duplicate":     {entry(a), entry(b), entry(a)},
		"missing":       {entry(a, "missing")},
		"self cycle":    {entry(a, "a")},
		"cycle":         {entry(a, "b"), entry(b, "a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := extension.New(entries...); err == nil {
				t.Fatal("accepted invalid graph")
			}
		})
	}
	assertEvents(t, events, nil)
}

func TestStartupFailureRollsBackAndSealsComposition(t *testing.T) {
	var events []string
	loadErr := errors.New("load failed")
	a := &testExtension{name: "a", events: &events}
	b := &testExtension{name: "b", events: &events, loadErr: loadErr}
	set := newSet(t, entry(a), entry(b, "a"))
	if err := set.Load(t.Context()); !errors.Is(err, loadErr) {
		t.Fatalf("load error = %v", err)
	}
	assertEvents(t, events, []string{"load:a", "load:b", "close:b", "close:a"})
	if err := set.Load(t.Context()); err == nil {
		t.Fatal("failed composition loaded again")
	}
}

func TestCloseFailureRetainsDependenciesUntilRetry(t *testing.T) {
	var events []string
	closeErr := errors.Join(extension.ErrCloseIncomplete, errors.New("still stopping"))
	a := &testExtension{name: "a", events: &events}
	b := &testExtension{name: "b", events: &events, closeErr: closeErr}
	c := &testExtension{name: "c", events: &events}
	set := newSet(t, entry(c), entry(a), entry(b, "a"))
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); !errors.Is(err, closeErr) {
		t.Fatalf("close error = %v", err)
	}
	assertEvents(t, events, []string{"load:c", "load:a", "load:b", "close:b", "close:c"})
	b.closeErr = nil
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:c", "load:a", "load:b", "close:b", "close:c", "close:b", "close:a"})
}

func TestCanceledLifecycleWaiterDoesNotMutateComposition(t *testing.T) {
	var events []string
	m := &testExtension{name: "m", events: &events, started: make(chan struct{}), release: make(chan struct{})}
	set := newSet(t, entry(m))
	loaded := make(chan error, 1)
	go func() { loaded <- set.Load(t.Context()) }()
	<-m.started
	waitCtx, cancel := context.WithCancel(t.Context())
	waiting := make(chan error, 1)
	go func() { waiting <- set.Load(waitCtx) }()
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting load = %v", err)
	}
	close(m.release)
	if err := <-loaded; err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:m", "close:m"})
}

func TestCompletedCloseErrorReleasesDependenciesWithoutRetry(t *testing.T) {
	var events []string
	want := errors.New("final flush failed after release")
	file := &testExtension{name: "file", events: &events}
	writer := &testExtension{name: "writer", events: &events, closeErr: want}
	set := newSet(t, entry(writer, "file"), entry(file))
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); !errors.Is(err, want) || errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("completed Close = %v", err)
	}
	wantEvents := []string{"load:file", "load:writer", "close:writer", "close:file"}
	assertEvents(t, events, wantEvents)
	if err := set.Close(t.Context()); err != nil {
		t.Fatalf("completed Close repeated error: %v", err)
	}
	assertEvents(t, events, wantEvents)
}

func TestNestedSetPropagatesCompletionAndRetainsBorrowedResource(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed error", true: "incomplete"}[incomplete], func(t *testing.T) {
			var events []string
			want := errors.New("child close failed")
			leaf := &testExtension{name: "leaf", events: &events, closeErr: want}
			if incomplete {
				leaf.closeErr = errors.Join(extension.ErrCloseIncomplete, want)
			}
			child := newSet(t, entry(leaf))
			resource := &testExtension{name: "resource", events: &events}
			parent := newSet(t, entry(resource), extension.Entry{
				ID: "child", DependsOn: []string{"resource"}, Extension: extension.Func{
					LoadFunc:  func(scope *extension.Context) error { return child.Load(scope.Init()) },
					CloseFunc: child.Close,
				},
			})
			if err := parent.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			err := parent.Close(t.Context())
			if !errors.Is(err, want) || errors.Is(err, extension.ErrCloseIncomplete) != incomplete {
				t.Fatalf("parent Close = %v", err)
			}
			if incomplete {
				assertEvents(t, events, []string{"load:resource", "load:leaf", "close:leaf"})
				leaf.closeErr = nil
			} else {
				assertEvents(t, events, []string{"load:resource", "load:leaf", "close:leaf", "close:resource"})
			}
			if err := parent.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if incomplete {
				assertEvents(t, events, []string{"load:resource", "load:leaf", "close:leaf", "close:leaf", "close:resource"})
			}
		})
	}
}

func TestCanceledCloseReportsIncompleteWithoutClosingExtensions(t *testing.T) {
	var events []string
	set := newSet(t, entry(&testExtension{name: "resource", events: &events}))
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := set.Close(ctx)
	if !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v", err)
	}
	assertEvents(t, events, []string{"load:resource"})
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []string{"load:resource", "close:resource"})
}

func TestEmptyAndIndependentCompositions(t *testing.T) {
	empty := newSet(t)
	if err := empty.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	var first, second []string
	a := newSet(t, entry(&testExtension{name: "same", events: &first}))
	b := newSet(t, entry(&testExtension{name: "same", events: &second}))
	if err := a.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(t.Context()); err != nil {
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
