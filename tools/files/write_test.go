package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// interruptedText supplies one successful chunk before failing. This exercises
// cleanup after bytes have reached a real temporary file, not just cancellation
// before admission or a manually manufactured Close timeout.
type interruptedText struct {
	chunkSent bool
	fail      func() error
}

func (r *interruptedText) Read(p []byte) (int, error) {
	if !r.chunkSent {
		r.chunkSent = true
		return copy(p, "partial replacement"), nil
	}
	return 0, r.fail()
}

func TestInterruptedWritePreservesDestination(t *testing.T) {
	for _, cancelCall := range []bool{false, true} {
		name := "read-error"
		if cancelCall {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errors.New("interrupted input")
			if cancelCall {
				want = context.Canceled
			}
			input := &interruptedText{fail: func() error {
				if cancelCall {
					cancel()
				}
				return want
			}}
			if _, err := write(ctx, root, "note.txt", input); !errors.Is(err, want) {
				t.Fatalf("interrupted write: %v", err)
			}
			if !input.chunkSent {
				t.Fatal("test did not reach partial writing")
			}
			data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
			if err != nil || string(data) != "original" {
				t.Fatalf("interrupted write replaced destination: %q, %v", data, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "note.txt" {
				t.Fatalf("partial file leaked: %v, %v", entries, err)
			}
		})
	}
}
