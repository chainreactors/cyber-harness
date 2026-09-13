package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListingAndGlobRespectRootAndCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.txt", "a.txt", "sub/c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := New(Config{Directory: dir}, nil)
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	entries, err := f.List(t.Context(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"a.txt", "b.txt", "sub"}) {
		t.Fatalf("listing: %v", names)
	}
	if _, err := readDirectory(t.Context(), f.root, ".", 2); err == nil {
		t.Fatal("unbounded directory allocation")
	}
	for _, tt := range []struct {
		pattern string
		limit   int
		want    []string
	}{
		{"*.txt", 10, []string{"a.txt", "b.txt"}},
		{"sub/*.txt", 10, []string{"sub/c.txt"}},
		{"*.txt", 1, []string{"a.txt"}},
	} {
		got, err := f.Glob(t.Context(), tt.pattern, tt.limit)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("glob %s: %v %v", tt.pattern, got, err)
		}
	}
	if _, err := f.Glob(t.Context(), "**/*.txt", 10); err == nil {
		t.Fatal("silently accepted unsupported recursive glob")
	}
	if _, err := f.List(t.Context(), ".."); err == nil {
		t.Fatal("listed outside root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Glob(ctx, "*", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("glob ignored cancellation: %v", err)
	}
	if _, err := f.List(ctx, "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("list ignored cancellation: %v", err)
	}
}
