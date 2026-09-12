package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	filepb "github.com/chainreactors/aiscan/aop/file"
)

func TestObservationReportsCommittedOperationsAndFailures(t *testing.T) {
	f, err := New(Config{Directory: t.TempDir(), MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	var got []observation
	handler := func(ctx context.Context, op filepb.AccessOp, path string, data []byte, size int64, err error, _ uint32) {
		if err == nil && ctx.Err() != nil {
			t.Errorf("successful operation delivered an already canceled context: %v", ctx.Err())
		}
		got = append(got, observation{ctx: ctx, op: op, path: path, data: append([]byte(nil), data...), size: size, err: err})
	}
	if _, err := f.Subscribe(handler); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("subscribe before Load: %v", err)
	}
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fSet.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	s, err := f.Subscribe(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err := f.Write(t.Context(), "note", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(t.Context(), "note", []byte("two")); err != nil {
		t.Fatal(err)
	}
	read, err := f.Read(t.Context(), "note")
	if err != nil {
		t.Fatal(err)
	}
	read[0] = 'x'
	if err := f.Write(t.Context(), "note", []byte("oversized")); err == nil {
		t.Fatal("oversized write succeeded")
	}
	if _, err := f.Read(t.Context(), "absent"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read: %v", err)
	}
	want := []filepb.AccessOp{filepb.AccessOp_ACCESS_OP_CREATE, filepb.AccessOp_ACCESS_OP_WRITE, filepb.AccessOp_ACCESS_OP_READ, filepb.AccessOp_ACCESS_OP_WRITE, filepb.AccessOp_ACCESS_OP_READ}
	if len(got) != len(want) {
		t.Fatalf("observations: %d", len(got))
	}
	for i, op := range want {
		if got[i].op != op || !filepath.IsAbs(got[i].path) {
			t.Fatalf("observation %d: %+v", i, got[i])
		}
		if i < 3 && (got[i].size != 3 || len(got[i].data) != 3 || got[i].err != nil) {
			t.Fatalf("successful operation %d: %+v", i, got[i])
		}
		if i >= 3 && (got[i].err == nil || len(got[i].data) != 0) {
			t.Fatalf("failure reported content: %+v", got[i])
		}
	}
	if string(got[2].data) != "two" {
		t.Fatal("observer did not copy borrowed bytes")
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(t.Context(), "note", []byte("last")); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal("callback after subscription Close")
	}
}

func TestCloseRetainsRootUntilObservationCompletes(t *testing.T) {
	f, err := New(Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := f.Subscribe(func(context.Context, filepb.AccessOp, string, []byte, int64, error, uint32) {
		close(entered)
		<-release
	})
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- f.Write(context.Background(), "note", []byte("committed")) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closeErr := f.Close(ctx)
	subscriptionErr := s.Close(ctx)
	// Always release the callback before assertions or cleanup.
	close(release)
	writeErr := <-writeDone
	if !errors.Is(closeErr, context.Canceled) {
		t.Fatalf("Close: %v", closeErr)
	}
	if !errors.Is(subscriptionErr, context.Canceled) {
		t.Fatalf("subscription Close: %v", subscriptionErr)
	}
	if writeErr != nil {
		t.Fatalf("committed write canceled retroactively: %v", writeErr)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Subscribe(func(context.Context, filepb.AccessOp, string, []byte, int64, error, uint32) {}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("subscribed to closed FS: %v", err)
	}
}
