package files

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	filepb "github.com/chainreactors/aiscan/aop/file"
)

func TestEditsUseOriginalAndRejectAmbiguousChanges(t *testing.T) {
	tests := []struct {
		name, original, want string
		edits                []EditPatch
	}{
		{"original matches", "red blue", "blue green", []EditPatch{{OldText: "red", NewText: "blue"}, {OldText: "blue", NewText: "green"}}},
		{"replace all", "a a a", "b b b", []EditPatch{{OldText: "a", NewText: "b", ReplaceAll: true}}},
		{"ambiguous", "a a", "", []EditPatch{{OldText: "a", NewText: "b"}}},
		{"overlap", "abcdef", "", []EditPatch{{OldText: "abc", NewText: "x"}, {OldText: "cde", NewText: "y"}}},
		{"missing", "abc", "", []EditPatch{{OldText: "z", NewText: "x"}}},
		{"empty original match", "abc", "", []EditPatch{{NewText: "x"}}},
		{"unchanged", "abc", "", []EditPatch{{OldText: "abc", NewText: "abc"}}},
		{"no edits", "abc", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyEdits(tt.original, tt.edits)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("accepted invalid edits: %q", got)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("result %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestEditBoundPreservesFileAndReportsOneOperation(t *testing.T) {
	f, err := New(Config{Directory: t.TempDir(), MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	fSet := filesystemSet(t, f)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	if err := f.Write(t.Context(), "note", []byte("aa")); err != nil {
		t.Fatal(err)
	}
	var observed []observation
	sub, err := f.Subscribe(func(ctx context.Context, op filepb.AccessOp, path string, data []byte, size int64, err error, edits uint32) {
		observed = append(observed, observation{op: op, data: append([]byte(nil), data...), err: err, edits: edits})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(context.Background())
	if err := f.Edit(t.Context(), "note", []EditPatch{{OldText: "a", NewText: strings.Repeat("x", 8), ReplaceAll: true}}); err == nil {
		t.Fatal("accepted oversized replacement")
	}
	if len(observed) != 1 || observed[0].op != filepb.AccessOp_ACCESS_OP_EDIT || observed[0].err == nil || observed[0].edits != 1 {
		t.Fatalf("failed edit observation: %+v", observed)
	}
	data, err := os.ReadFile(f.config.Directory + string(os.PathSeparator) + "note")
	if err != nil || string(data) != "aa" {
		t.Fatalf("failed edit changed file: %q %v", data, err)
	}
	observed = nil
	if err := f.Edit(t.Context(), "note", []EditPatch{{OldText: "aa", NewText: "done"}}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].op != filepb.AccessOp_ACCESS_OP_EDIT || observed[0].err != nil || string(observed[0].data) != "done" {
		t.Fatalf("successful edit observation: %+v", observed)
	}
	ro, _ := New(Config{Directory: f.config.Directory, ReadOnly: true})
	roSet := filesystemSet(t, ro)
	if err := roSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer roSet.Close(context.Background())
	if err := ro.Edit(t.Context(), "note", []EditPatch{{OldText: "done", NewText: "bad"}}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("read-only edit: %v", err)
	}
}

func TestEditFinalSizeIsIndependentOfPatchOrder(t *testing.T) {
	edits := []EditPatch{{OldText: "a", NewText: "bbbb"}, {OldText: "long", NewText: ""}}
	got, err := applyEdits("a long", edits, 5)
	if err != nil || got != "bbbb " {
		t.Fatalf("result: %q, %v", got, err)
	}
}
