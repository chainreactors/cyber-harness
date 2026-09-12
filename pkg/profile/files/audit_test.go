package files_test

import (
	"context"
	"errors"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	"github.com/chainreactors/aiscan/tools/files"
)

// This composition intentionally lives in an external test: the minimal
// profile must not import Audit merely to let another composition select it.
func auditedFiles(t *testing.T) (*extension.Set, tool.Executor, *fileext.Extension, *fileaudit.Audit) {
	t.Helper()
	f, err := fileext.New(files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := fileaudit.NewWithFiles(f)
	if err != nil {
		t.Fatal(err)
	}
	s, err := extension.New(
		extension.Entry{ID: "fs", Extension: f},
		extension.Entry{ID: "audit", DependsOn: []string{"fs"}, Extension: a},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, s.Executor(), f, a
}

func TestFileAuditSubscribesWithoutToolInjection(t *testing.T) {
	s, r, f, a := auditedFiles(t)
	var got []*filepb.Access
	sub := a.Subscribe(func(access *filepb.Access) { got = append(got, access) })
	defer sub.Close(context.Background())
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := tool.ContextWithInvocation(t.Context(), tool.Invocation{CallID: "file-call", WorkDir: "invocation-dir"})
	if _, err := r.ExecuteTool(ctx, "write", `{"path":"note","content":"first"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(ctx, "read", `{"path":"note"}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(ctx, "note", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(ctx, "absent"); err == nil {
		t.Fatal("missing file read succeeded")
	}
	// Disabling collection preserves file operations without constructing
	// records or hashing contents in the observer.
	a.Configure(&filepb.WatchConfig{Enabled: false})
	if err := f.Write(ctx, "note", []byte("disabled")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Set.Close is the publication barrier; no sleep or concurrent slice read.
	if len(got) != 4 {
		t.Fatalf("observations = %d, want 4", len(got))
	}
	want := []filepb.AccessOp{filepb.AccessOp_ACCESS_OP_CREATE, filepb.AccessOp_ACCESS_OP_READ, filepb.AccessOp_ACCESS_OP_WRITE, filepb.AccessOp_ACCESS_OP_READ}
	for i, access := range got {
		if access.Op != want[i] || access.ToolId != "file-call" || access.WorkDir != "invocation-dir" || access.Id == "" || access.Timestamp == nil || !filepath.IsAbs(access.Path) {
			t.Fatalf("observation %d: %v", i, access)
		}
	}
	if got[0].Bytes != 5 || got[1].Bytes != 5 || got[2].Bytes != 6 || got[1].Size != 5 {
		t.Fatalf("wrong actual byte counts: %v", got)
	}
	if got[0].Digest != fileaudit.Digest([]byte("first")) || got[2].Digest != fileaudit.Digest([]byte("second")) || got[1].Digest != "" {
		t.Fatal("digest does not describe committed bytes")
	}
	if got[3].Error == "" || got[3].Bytes != 0 || got[3].Digest != "" {
		t.Fatalf("failed read: %v", got[3])
	}
}

func TestFileAuditCloseTimeoutRetainsFileDependency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, r, f, a := auditedFiles(t)
		entered, release := make(chan struct{}), make(chan struct{})
		sub := a.Subscribe(func(*filepb.Access) { close(entered); <-release })
		defer sub.Close(context.Background())
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"note","content":"done"}`); err != nil {
			t.Fatal(err)
		}
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeErr := s.Close(ctx)
		retained := f.Ready()
		_, executeErr := r.ExecuteTool(t.Context(), "write", `{"path":"late","content":"no"}`)
		close(release)
		if !errors.Is(closeErr, extension.ErrCloseIncomplete) || !errors.Is(closeErr, context.DeadlineExceeded) {
			t.Fatalf("Close: %v", closeErr)
		}
		if !retained {
			t.Fatal("released FS while audit publication was incomplete")
		}
		if executeErr == nil {
			t.Fatal("file tools admitted work during Close")
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if f.Ready() {
			t.Fatal("file dependency survived successful retry")
		}

	})
}

func TestRemovingAuditLeavesFileServiceUsable(t *testing.T) {
	f, err := fileext.New(files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := fileaudit.NewWithFiles(f)
	if err != nil {
		t.Fatal(err)
	}
	fsSet, err := extension.New(extension.Entry{ID: "filesystem", Extension: f})
	if err != nil {
		t.Fatal(err)
	}
	auditSet, err := extension.New(extension.Entry{ID: "audit", Extension: a})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsSet.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := auditSet.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := fsSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := auditSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got []*filepb.Access
	sub := a.Subscribe(func(access *filepb.Access) { got = append(got, access) })
	defer sub.Close(context.Background())
	if err := f.Write(t.Context(), "note", []byte("observed")); err != nil {
		t.Fatal(err)
	}
	if err := auditSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(t.Context(), "note", []byte("unobserved")); err != nil {
		t.Fatal(err)
	}
	data, err := f.Read(t.Context(), "note")
	if err != nil || string(data) != "unobserved" {
		t.Fatalf("read after audit Close: %q, %v", data, err)
	}
	if len(got) != 1 {
		t.Fatalf("callbacks after Close: %d", len(got))
	}
	if err := auditSet.Load(t.Context()); err == nil {
		t.Fatal("reloaded a closed audit instance")
	}
	if err := fsSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAuditsRemainIsolatedBetweenFileServices(t *testing.T) {
	s1, _, f1, a1 := auditedFiles(t)
	s2, _, f2, a2 := auditedFiles(t)
	var first, second []*filepb.Access
	sub1 := a1.Subscribe(func(access *filepb.Access) { first = append(first, access) })
	defer sub1.Close(context.Background())
	sub2 := a2.Subscribe(func(access *filepb.Access) { second = append(second, access) })
	defer sub2.Close(context.Background())
	if err := s1.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s2.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f1.Write(t.Context(), "note", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := f2.Write(t.Context(), "note", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s2.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("cross-instance observations: %d, %d", len(first), len(second))
	}
	if first[0].Path == second[0].Path || first[0].Id == second[0].Id || first[0].Digest == second[0].Digest {
		t.Fatal("audit state leaked between instances")
	}
}
