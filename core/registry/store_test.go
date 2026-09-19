package registry

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestStorePublishesAndHotAddsBatches(t *testing.T) {
	store := New[string]()
	first, err := store.Add(
		Value[string]{Name: "one", Value: "first"},
		Value[string]{Name: "two", Value: "second"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("one"); ok {
		t.Fatal("collecting store published values")
	}
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	late, err := store.Add(Value[string]{Name: "late", Value: "late"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.Names(), []string{"one", "two", "late"}) {
		t.Fatalf("names = %v", store.Names())
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.Names(), []string{"late"}) {
		t.Fatalf("names after retract = %v", store.Names())
	}
	if err := late.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBatchCloseCancelsOnlyItsCallsAndDrains(t *testing.T) {
	store := New[string]()
	hold, _ := store.Add(Value[string]{Name: "hold", Value: "value"})
	keep, _ := store.Add(Value[string]{Name: "keep", Value: "value"})
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, call, release, err := store.Acquire(context.Background(), "hold")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hold.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("close before release = %v", err)
	}
	if !errors.Is(call.Err(), context.Canceled) {
		t.Fatalf("call context = %v", call.Err())
	}
	if _, _, _, err := store.Acquire(t.Context(), "hold"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("removed admission = %v", err)
	}
	if _, _, releaseKeep, err := store.Acquire(t.Context(), "keep"); err != nil {
		t.Fatalf("unrelated admission = %v", err)
	} else {
		releaseKeep()
	}
	release()
	if err := hold.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := keep.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsDuplicateBatchAtomically(t *testing.T) {
	store := New[int]()
	if _, err := store.Add(Value[int]{Name: "one", Value: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(Value[int]{Name: "two", Value: 2}, Value[int]{Name: "one", Value: 3}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := store.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.Names(), []string{"one"}) {
		t.Fatalf("names = %v", store.Names())
	}
}
