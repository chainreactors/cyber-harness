package registry

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestStoreRegistersAtomicallyAndPublishesOnActivate(t *testing.T) {
	store := New[string]()
	retract, err := store.Register("test", "shared",
		Value[string]{Name: "one", Value: "first"},
		Value[string]{Name: "two", Value: "second"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("one"); ok || len(store.Names()) != 0 {
		t.Fatal("collecting registry published values")
	}
	if _, err := store.Register("test", "shared",
		Value[string]{Name: "fresh", Value: "fresh"},
		Value[string]{Name: "one", Value: "duplicate"},
	); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate registration = %v", err)
	}
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.Names(), []string{"one", "two"}) || !slices.Equal(store.GroupNames("shared"), []string{"one", "two"}) {
		t.Fatalf("published names=%v group=%v", store.Names(), store.GroupNames("shared"))
	}
	if _, err := store.Register("test", "", Value[string]{Name: "late", Value: "late"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("late registration = %v", err)
	}
	retract()
	// Retraction cannot mutate an active registry. It is owned by an Extension
	// and normally runs only after the dependent registry has closed.
	retract()
	if !slices.Equal(store.Names(), []string{"one", "two"}) {
		t.Fatal("active registry was mutated by retraction")
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCloseCancelsAndDrainsAcquiredCalls(t *testing.T) {
	store := New[string]()
	if _, err := store.Register("test", "", Value[string]{Name: "hold", Value: "value"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry, call, release, err := store.Acquire(context.Background(), "hold")
	if err != nil || entry.Value != "value" {
		t.Fatalf("acquire = %+v, %v", entry, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close before release = %v", err)
	}
	if !errors.Is(call.Err(), context.Canceled) {
		t.Fatalf("acquired context = %v", call.Err())
	}
	if _, _, _, err := store.Acquire(t.Context(), "hold"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admission survived close = %v", err)
	}
	release()
	release()
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRetractsFailedBatchBeforeActivation(t *testing.T) {
	store := New[int]()
	retract, err := store.Register("test", "group", Value[int]{Name: "one", Value: 1})
	if err != nil {
		t.Fatal(err)
	}
	retract()
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(store.Entries()) != 0 || len(store.GroupNames("group")) != 0 {
		t.Fatal("retracted values were published")
	}
}
