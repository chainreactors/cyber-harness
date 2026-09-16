package extension_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
)

type testExtension struct {
	name       string
	events     *[]string
	loadErr    error
	incomplete bool
}

func (e *testExtension) Load(*extension.Scope) error {
	*e.events = append(*e.events, "load "+e.name)
	return e.loadErr
}

func (e *testExtension) Close(context.Context) error {
	*e.events = append(*e.events, "close "+e.name)
	if e.incomplete {
		e.incomplete = false
		return extension.ErrCloseIncomplete
	}
	return nil
}

func TestSetLoadsAndClosesLinearly(t *testing.T) {
	var events []string
	set, err := extension.New(
		&testExtension{name: "one", events: &events},
		&testExtension{name: "two", events: &events},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !set.Active() {
		t.Fatal("set is not active")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"load one", "load two", "close two", "close one"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestLoadFailureRollsBackIncludingFailingExtension(t *testing.T) {
	var events []string
	set, _ := extension.New(
		&testExtension{name: "one", events: &events},
		&testExtension{name: "two", events: &events, loadErr: errors.New("boom")},
	)
	if err := set.Load(context.Background()); err == nil {
		t.Fatal("expected load error")
	}
	want := []string{"load one", "load two", "close two", "close one"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

type loadOnlyExtension struct{ loaded bool }

func (e *loadOnlyExtension) Load(*extension.Scope) error {
	e.loaded = true
	return nil
}

func TestLoadOnlyExtensionNeedsNoEmptyClose(t *testing.T) {
	value := &loadOnlyExtension{}
	set, err := extension.New(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !value.loaded {
		t.Fatal("extension was not loaded")
	}
}

func TestCloseBeforeLoadDoesNotTouchExtensions(t *testing.T) {
	var events []string
	set, err := extension.New(&testExtension{name: "unused", events: &events})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %v", events)
	}
}

func TestCloseStopsAtIncompleteAndRetriesSameExtension(t *testing.T) {
	var events []string
	set, _ := extension.New(
		&testExtension{name: "dependency", events: &events},
		&testExtension{name: "consumer", events: &events, incomplete: true},
	)
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(context.Background()); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("first close = %v", err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"close consumer", "close consumer", "close dependency"}
	if !reflect.DeepEqual(events[len(events)-3:], wantTail) {
		t.Fatalf("events = %#v", events)
	}
}

type stringPoint struct{ values []string }

func (p *stringPoint) Add(values ...string) (resource.Handle, error) {
	p.values = append(p.values, values...)
	return closeFunc(func(context.Context) error {
		p.values = p.values[:len(p.values)-len(values)]
		return nil
	}), nil
}

type closeFunc func(context.Context) error

func (f closeFunc) Close(ctx context.Context) error { return f(ctx) }

func TestScopeRetractsResourcesBeforeExtensionClose(t *testing.T) {
	point := &stringPoint{}
	var saw int
	provider := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Define[string](scope, point)
	}}
	consumer := extension.Func{
		LoadFunc:  func(scope *extension.Scope) error { return extension.Add(scope, "value") },
		CloseFunc: func(context.Context) error { saw = len(point.values); return nil },
	}
	set, _ := extension.New(provider, consumer)
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saw != 0 {
		t.Fatalf("extension Close saw %d registered values", saw)
	}
}

type blockingStringPoint struct {
	mu      sync.Mutex
	values  []string
	entered chan struct{}
	proceed chan struct{}
}

func (p *blockingStringPoint) Add(values ...string) (resource.Handle, error) {
	close(p.entered)
	<-p.proceed
	p.mu.Lock()
	p.values = append(p.values, values...)
	p.mu.Unlock()
	return closeFunc(func(context.Context) error {
		p.mu.Lock()
		p.values = p.values[:len(p.values)-len(values)]
		p.mu.Unlock()
		return nil
	}), nil
}

func TestCloseWaitsForConcurrentRegistrationOwnership(t *testing.T) {
	point := &blockingStringPoint{entered: make(chan struct{}), proceed: make(chan struct{})}
	var consumerScope *extension.Scope
	provider := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Define[string](scope, point)
	}}
	consumer := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		consumerScope = scope
		return nil
	}}
	set, err := extension.New(provider, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	addDone := make(chan error, 1)
	go func() { addDone <- extension.Add(consumerScope, "late") }()
	<-point.entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- set.Close(context.Background()) }()
	deadline := time.After(time.Second)
	for set.Active() {
		select {
		case <-deadline:
			t.Fatal("close did not start")
		default:
		}
	}
	close(point.proceed)
	if err := <-addDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	point.mu.Lock()
	defer point.mu.Unlock()
	if len(point.values) != 0 {
		t.Fatalf("values after close = %v", point.values)
	}
}
