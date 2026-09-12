package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestFSOwnsBytesAndEnforcesReadOnly(t *testing.T) {
	dir := t.TempDir()
	f, err := New(Config{Directory: dir, MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if f.Ready() {
		t.Fatal("constructor opened root")
	}
	if _, err := f.Read(t.Context(), "file"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read before Load: %v", err)
	}
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	want := []byte{0, 0xff, 2}
	if err := f.Write(t.Context(), "file", want); err != nil {
		t.Fatal(err)
	}
	data, err := f.Read(t.Context(), "file")
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("binary read: %v, %v", data, err)
	}
	data[0] = 1
	again, err := f.Read(t.Context(), "file")
	if err != nil || !bytes.Equal(again, want) {
		t.Fatalf("read bytes are not owned: %v, %v", again, err)
	}
	readonly, err := New(Config{Directory: dir, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	readonlySet := filesystemSet(t, readonly)
	if err := readonlySet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer readonlySet.Close(context.Background())
	if err := readonly.Write(t.Context(), "file", []byte("new")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("direct write bypassed policy: %v", err)
	}
	if err := f.Write(t.Context(), "file", []byte("large")); err == nil {
		t.Fatal("accepted oversized write")
	}
	if err := f.Write(t.Context(), "../outside", nil); err == nil {
		t.Fatal("accepted nonlocal write")
	}
	if _, err := f.Read(t.Context(), "."); err == nil {
		t.Fatal("accepted nonregular read")
	}
	final, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || !bytes.Equal(final, want) {
		t.Fatalf("rejected write modified file: %v, %v", final, err)
	}
}

func TestFSFailedLoadAndCloseAreFinal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	f, err := New(Config{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor touched filesystem: %v", err)
	}
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err == nil {
		t.Fatal("loaded absent root")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fSet.Load(t.Context()); err == nil {
		t.Fatalf("reused failed instance: %v", err)
	}
	if err := fSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.Ready() {
		t.Fatal("failed service is ready")
	}
}

func TestFSCloseTimeoutRetainsAdmittedRoot(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		f, err := New(Config{Directory: dir})
		if err != nil {
			t.Fatal(err)
		}
		fSet := filesystemSet(t, f)
		if err := fSet.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Hold the same root admission used by Read/Write, so the test can
		// deterministically delay resource release without a slow syscall.
		root, err := f.acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var release sync.Once
		defer func() { release.Do(f.release); _ = fSet.Close(context.Background()) }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := fSet.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close: %v", err)
		}
		if f.Ready() || !errors.Is(f.lifetime.Err(), context.Canceled) {
			t.Fatal("Close did not stop admission and cancel operations")
		}
		if _, err := f.Read(t.Context(), "file"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("admitted after Close: %v", err)
		}
		if _, err := root.Stat("."); err != nil {
			t.Fatalf("closed admitted root prematurely: %v", err)
		}
		release.Do(f.release)
		if err := fSet.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := root.Stat("."); err == nil {
			t.Fatal("root handle survived successful Close")
		}
		if err := fSet.Load(t.Context()); err == nil {
			t.Fatalf("reloaded closed root: %v", err)
		}
	})
}

func TestFSCanceledInitializationDoesNotOwnLifetime(t *testing.T) {
	f, err := New(Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fSet := filesystemSet(t, f)
	if err := fSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	cancel()
	if err := f.Write(t.Context(), "still-active", []byte("yes")); err != nil {
		t.Fatalf("initialization context owns FS lifetime: %v", err)
	}
	if err := f.Write(ctx, "still-active", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored canceled operation: %v", err)
	}
}
