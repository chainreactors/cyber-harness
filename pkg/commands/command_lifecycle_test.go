package commands

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
)

func TestCommandRegistrationsAreGroupedAndImmutable(t *testing.T) {
	run := func(context.Context, *Execution) (any, error) { return "ok", nil }
	r, _ := loadTestRegistry(t,
		commandGroup("first", "shared", Command{Name: "one", Run: run}),
		commandGroup("second", "shared", Command{Name: "two", Run: run}),
	)
	if got := r.GroupNames("shared"); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("group names = %v", got)
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
	var retained *extension.Scope
	first := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		retained = scope
		return r.Register(scope, "shared", Command{Name: "one", Run: func(context.Context, *Execution) (any, error) { return nil, nil }})
	}}
	registrySet, err := extension.New(
		extension.Entry{ID: "first", Extension: first},
		extension.Entry{ID: "registry", DependsOn: []string{"first"}, Extension: r},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registrySet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer registrySet.Close(context.Background())
	if err := r.Register(retained, "shared", Command{Name: "fresh", Run: func(context.Context, *Execution) (any, error) { return nil, nil }}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("registration after activation = %v", err)
	}
	if r.Has("fresh") {
		t.Fatal("post-activation registration became visible")
	}

	failed := NewRegistry(nil)
	duplicate := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return failed.Register(scope, "group",
			Command{Name: "same", Run: func(context.Context, *Execution) (any, error) { return nil, nil }},
			Command{Name: "same", Run: func(context.Context, *Execution) (any, error) { return nil, nil }},
		)
	}}
	failedSet, err := extension.New(
		extension.Entry{ID: "duplicate", Extension: duplicate},
		extension.Entry{ID: "registry", DependsOn: []string{"duplicate"}, Extension: failed},
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
	r, _ := loadTestRegistry(t, commandGroup("owner", "group", Command{Name: "hold", Run: func(ctx context.Context, _ *Execution) (any, error) {
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
	r, _ := loadTestRegistry(t, commandGroup("owner", "group", Command{Name: "panic", Run: func(context.Context, *Execution) (any, error) { panic("test") }}))
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
			return r.Register(scope, "group", Command{Name: name, Run: func(context.Context, *Execution) (any, error) { return nil, nil }})
		}}
		set, err := extension.New(
			extension.Entry{ID: "owner", Extension: contributor},
			extension.Entry{ID: "registry", DependsOn: []string{"owner"}, Extension: r},
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
