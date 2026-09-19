package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	filepb "github.com/chainreactors/cyber/aop/file"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func TestFileHookReportsCommittedOperationsAndFailures(t *testing.T) {
	registry := corehooks.New()
	resource, err := New(Config{Directory: t.TempDir(), MaxBytes: 4}, registry)
	if err != nil {
		t.Fatal(err)
	}
	f := resource.Files
	var got []toolhooks.FileEvent
	sub := toolhooks.FileAccessObserved.On(registry, "test", func(ctx context.Context, event toolhooks.FileEvent) (struct{}, error) {
		if event.Err == nil && ctx.Err() != nil {
			t.Errorf("successful operation delivered canceled context: %v", ctx.Err())
		}
		event.Data = append([]byte(nil), event.Data...)
		got = append(got, event)
		return struct{}{}, nil
	})
	defer sub.Close(context.Background())
	fSet := filesystemSet(t, resource)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())

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
		if got[i].Op != op || !filepath.IsAbs(got[i].Path) || got[i].Operation.GetOperationId() == "" {
			t.Fatalf("observation %d: %+v", i, got[i])
		}
		if i < 3 && (got[i].Size != 3 || len(got[i].Data) != 3 || got[i].Err != nil) {
			t.Fatalf("successful operation %d: %+v", i, got[i])
		}
		if i >= 3 && (got[i].Err == nil || len(got[i].Data) != 0) {
			t.Fatalf("failure reported content: %+v", got[i])
		}
	}
	if string(got[2].Data) != "two" {
		t.Fatal("hook did not copy callback-scoped bytes")
	}
}

func TestCloseRetainsRootUntilFileHookCompletes(t *testing.T) {
	registry := corehooks.New()
	resource, err := New(Config{Directory: t.TempDir()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	f := resource.Files
	fSet := filesystemSet(t, resource)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	sub := toolhooks.FileAccessObserved.On(registry, "test", func(context.Context, toolhooks.FileEvent) (struct{}, error) {
		close(entered)
		<-release
		return struct{}{}, nil
	})
	writeDone := make(chan error, 1)
	go func() { writeDone <- f.Write(context.Background(), "note", []byte("committed")) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closeErr := resource.Close(ctx)
	subscriptionErr := sub.Close(ctx)
	close(release)
	writeErr := <-writeDone
	if !errors.Is(closeErr, context.Canceled) || !errors.Is(subscriptionErr, context.Canceled) {
		t.Fatalf("close=%v subscription=%v", closeErr, subscriptionErr)
	}
	if writeErr != nil {
		t.Fatalf("committed write canceled retroactively: %v", writeErr)
	}
	if err := sub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
