package commands

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"slices"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
)

func TestCommandOwnersAreIndependentOfGroups(t *testing.T) {
	r := NewRegistry()
	defer r.Close(context.Background())
	run := func(context.Context, *Execution) (any, error) { return "ok", nil }
	if err := r.Register("first", "shared", Command{Name: "one", Run: run}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("second", "shared", Command{Name: "two", Run: run}); err != nil {
		t.Fatal(err)
	}
	cached, ok := r.Get("one")
	if !ok {
		t.Fatal("missing metadata")
	}
	cached.Usage = "caller mutation"
	if current, _ := r.Get("one"); current.Usage != "" {
		t.Fatal("discovery leaked mutable registry state")
	}
	if err := r.Register("failed", "shared", Command{Name: "fresh"}, Command{Name: "one"}); !errors.Is(err, ErrDuplicateCommand) {
		t.Fatalf("atomic registration: %v", err)
	}
	if r.Has("fresh") {
		t.Fatal("partially published failed registration")
	}
	if err := r.UnregisterOwner(t.Context(), "failed"); !errors.Is(err, ErrUnknownOwner) {
		t.Fatalf("failed install acquired ownership: %v", err)
	}
	if err := r.UnregisterOwner(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if got := r.GroupNames("shared"); !slices.Equal(got, []string{"two"}) {
		t.Fatalf("same-group owner removed: %v", got)
	}
	if _, err := r.Execute(t.Context(), cached.Name, &Execution{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cached discovery admitted execution: %v", err)
	}
	if result, err := r.Execute(t.Context(), "two", &Execution{}); err != nil || result != "ok" {
		t.Fatalf("unrelated owner unavailable: %v, %v", result, err)
	}
	if r.items["one"].Run != nil {
		t.Fatal("retired owner retained executable closure")
	}
	if err := r.Register("replacement", "shared", Command{Name: "one", Run: run}); !errors.Is(err, ErrDuplicateCommand) {
		t.Fatalf("reused a retired name: %v", err)
	}
	if err := r.Register("first", "shared", Command{Name: "another", Run: run}); !errors.Is(err, ErrDuplicateCommand) {
		t.Fatalf("reused a retired owner: %v", err)
	}
}

func TestCommandUnregisterCancelsAndWaitsForActualReturn(t *testing.T) {
	r := NewRegistry()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if err := r.Register("owner", "group", Command{Name: "hold", Run: func(ctx context.Context, _ *Execution) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() { _, err := r.Execute(context.Background(), "hold", &Execution{}); callDone <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.UnregisterOwner(ctx, "owner")
	<-canceled
	_, rejected := r.Execute(t.Context(), "hold", &Execution{})
	visible := r.Has("hold")
	close(release)
	callErr := <-callDone
	if !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.Canceled) {
		t.Fatalf("premature unregister: %v", err)
	}
	if visible || !errors.Is(rejected, ErrUnavailable) {
		t.Fatalf("admission survived revocation: %v, %v", visible, rejected)
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("owner did not cancel invocation: %v", callErr)
	}
	if err := r.UnregisterOwner(t.Context(), "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	rSet := extensiontest.Set(t, extension.Entry{ID: "r", Extension: r})
	if err := rSet.Load(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reloaded closed registry: %v", err)
	}
}

func TestRegistryCloseCancelsAllOwnersBeforeWaiting(t *testing.T) {
	r := NewRegistry()
	entered := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		if err := r.Register(name, "group", Command{Name: name, Run: func(ctx context.Context, _ *Execution) (any, error) {
			entered <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			<-release
			return nil, ctx.Err()
		}}); err != nil {
			t.Fatal(err)
		}
		go func() { _, err := r.Execute(context.Background(), name, &Execution{}); done <- err }()
	}
	<-entered
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Close(ctx)
	<-canceled
	<-canceled
	close(release)
	<-done
	<-done
	if !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("Close did not retain active owners: %v", err)
	}
	if len(r.All()) != 0 || len(r.Names()) != 0 {
		t.Fatal("closed registry still advertised commands")
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("new", "group", Command{Name: "new"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("registered after Close: %v", err)
	}
}

func TestCommandPanicReleasesAdmission(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("owner", "group", Command{Name: "panic", Run: func(context.Context, *Execution) (any, error) { panic("test") }}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(t.Context(), "panic", &Execution{}); err == nil || !strings.Contains(err.Error(), "command panic") {
		t.Fatalf("panic boundary: %v", err)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatalf("panic leaked admission: %v", err)
	}
}

func TestCommandRegistrationRejectsAmbiguousNames(t *testing.T) {
	r := NewRegistry()
	defer r.Close(context.Background())
	for _, name := range []string{"", " name", "name ", "two names", "tab\tname"} {
		if err := r.Register("owner", "group", Command{Name: name}); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	if err := r.Register("owner", "group", Command{Name: "valid"}); err != nil {
		t.Fatalf("invalid registration acquired owner: %v", err)
	}
}
