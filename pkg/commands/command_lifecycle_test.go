package commands

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
)

func TestCommandRegistrationsPreserveOrderAndMetadataSnapshots(t *testing.T) {
	run := func(context.Context, *Execution) (any, error) { return "ok", nil }
	r, _ := loadTestRegistry(t,
		commandBatch(Command{Name: "one", Run: run}),
		commandBatch(Command{Name: "two", Run: run}),
	)
	if got := r.Names(); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("command names = %v", got)
	}
	cached, ok := r.Get("one")
	if !ok {
		t.Fatal("missing command metadata")
	}
	cached.Usage = "caller mutation"
	if current, _ := r.Get("one"); current.Usage != "" {
		t.Fatal("discovery leaked mutable registry state")
	}
	if result, err := r.Execute(t.Context(), "two", &Execution{}); err != nil || result != "ok" {
		t.Fatalf("execute = %v, %v", result, err)
	}
}

func TestCommandRegistrationIsAtomicAndRegistrySeals(t *testing.T) {
	r := NewRegistry(nil)
	first := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope, Command{Name: "one", Run: func(context.Context, *Execution) (any, error) { return nil, nil }})
	}}
	registrySet, err := extension.New(
		r,
		first,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registrySet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer registrySet.Close(context.Background())
	hot, err := r.Add(Command{Name: "fresh", Run: func(context.Context, *Execution) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatalf("hot registration = %v", err)
	}
	if !r.Has("fresh") {
		t.Fatal("hot registration was not visible")
	}
	if err := hot.Close(t.Context()); err != nil || r.Has("fresh") {
		t.Fatalf("hot unregister = %v", err)
	}

	failed := NewRegistry(nil)
	duplicate := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope,
			Command{Name: "same", Run: func(context.Context, *Execution) (any, error) { return nil, nil }},
			Command{Name: "same", Run: func(context.Context, *Execution) (any, error) { return nil, nil }},
		)
	}}
	failedSet, err := extension.New(
		failed,
		duplicate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := failedSet.Load(t.Context()); !errors.Is(err, ErrDuplicateCommand) {
		t.Fatalf("duplicate load = %v", err)
	}
	if failed.Has("same") || len(failed.Names()) != 0 {
		t.Fatal("failed registration partially published")
	}
	if err := failedSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryCloseCancelsAndWaitsForActualReturn(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	r, _ := loadTestRegistry(t, commandBatch(Command{Name: "hold", Run: func(ctx context.Context, _ *Execution) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}}))
	callDone := make(chan error, 1)
	go func() { _, err := r.Execute(context.Background(), "hold", &Execution{}); callDone <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Close(ctx)
	<-canceled
	_, rejected := r.Execute(t.Context(), "hold", &Execution{})
	close(release)
	callErr := <-callDone
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("premature close = %v", err)
	}
	if !errors.Is(rejected, ErrUnavailable) {
		t.Fatalf("admission survived close: %v", rejected)
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("call was not canceled: %v", callErr)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCommandPanicReleasesAdmission(t *testing.T) {
	r, _ := loadTestRegistry(t, commandBatch(Command{Name: "panic", Run: func(context.Context, *Execution) (any, error) { panic("test") }}))
	if _, err := r.Execute(t.Context(), "panic", &Execution{}); err == nil || !strings.Contains(err.Error(), "command panic") {
		t.Fatalf("panic boundary: %v", err)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatalf("panic leaked admission: %v", err)
	}
}

func TestCommandRegistrationRejectsAmbiguousNames(t *testing.T) {
	for _, name := range []string{"", " name", "name ", "two names", "tab\tname"} {
		name := name
		r := NewRegistry(nil)
		contributor := extension.Func{LoadFunc: func(scope *extension.Scope) error {
			return extension.Add(scope, Command{Name: name, Run: func(context.Context, *Execution) (any, error) { return nil, nil }})
		}}
		set, err := extension.New(
			r,
			contributor,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := set.Load(t.Context()); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("name %q: %v", name, err)
		}
		if err := set.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
