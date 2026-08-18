package commands

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	filepb "github.com/chainreactors/aiscan/aop/file"
	coretool "github.com/chainreactors/aiscan/core/tool"
)

// collector drains an audit trail into a slice. Observations are published on
// the audit's own goroutine, so every assertion waits for the expected count
// rather than reading immediately after the call that produced it.
type collector struct {
	mu       sync.Mutex
	accesses []*filepb.Access
	detach   func()
}

func collect(t *testing.T, audit *FileAudit) *collector {
	t.Helper()
	c := &collector{}
	c.detach = audit.Subscribe(func(access *filepb.Access) {
		c.mu.Lock()
		c.accesses = append(c.accesses, access)
		c.mu.Unlock()
	})
	t.Cleanup(func() { c.detach() })
	return c
}

func (c *collector) wait(t *testing.T, count int) []*filepb.Access {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		got := append([]*filepb.Access(nil), c.accesses...)
		c.mu.Unlock()
		if len(got) >= count {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d observations, got %d", count, len(got))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (c *collector) byPath(t *testing.T, path string) *filepb.Access {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, access := range c.accesses {
		if access.GetPath() == path {
			return access
		}
	}
	t.Fatalf("no observation for %s", path)
	return nil
}

func TestNilFileAuditIsANoOp(t *testing.T) {
	var audit *FileAudit
	// Every entry point must survive a runtime that never wired one up.
	audit.Record(context.Background(), &filepb.Access{Path: "/tmp/x"})
	audit.RecordFile(context.Background(), filepb.AccessOp_ACCESS_OP_READ, "/tmp/x", nil)
	audit.Configure(&filepb.WatchConfig{Enabled: true})
	if audit.Enabled() {
		t.Fatal("a nil audit must not report itself as enabled")
	}
	ran := false
	if err := audit.Around(context.Background(), t.TempDir(), func() error { ran = true; return nil }); err != nil {
		t.Fatalf("Around: %v", err)
	}
	if !ran {
		t.Fatal("Around must still run the work when nothing is observing")
	}
}

func TestRecordFillsIdentityAndInvocation(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{CallID: "call-1", WorkDir: "/work"})
	audit.Record(ctx, &filepb.Access{Op: filepb.AccessOp_ACCESS_OP_READ, Path: "/work/a.txt"})

	access := c.wait(t, 1)[0]
	if access.GetToolId() != "call-1" {
		t.Fatalf("tool id = %q, want the invocation's call id", access.GetToolId())
	}
	if access.GetWorkDir() != "/work" {
		t.Fatalf("work dir = %q", access.GetWorkDir())
	}
	if access.GetId() == "" || access.GetTimestamp() == nil {
		t.Fatalf("identity and timing must be filled in: %+v", access)
	}
}

func TestDisabledAuditRecordsNothing(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	audit.Configure(&filepb.WatchConfig{Enabled: false})
	audit.Record(context.Background(), &filepb.Access{Path: "/tmp/x"})
	if state := audit.State(); state.GetWatching() {
		t.Fatal("state must report that observation is off")
	}

	time.Sleep(50 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.accesses) != 0 {
		t.Fatalf("expected nothing while disabled, got %d", len(c.accesses))
	}
}

func TestSnapshotDiffReportsCreateWriteDelete(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.txt")
	doomed := filepath.Join(dir, "doomed.txt")
	if err := os.WriteFile(kept, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doomed, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}

	options := defaultAuditOptions()
	before, err := TakeSnapshot(dir, options)
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	born := filepath.Join(dir, "born.txt")
	if err := os.WriteFile(born, []byte("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("one and a half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(doomed); err != nil {
		t.Fatal(err)
	}

	after, err := TakeSnapshot(dir, options)
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	want := map[string]filepb.AccessOp{
		born:   filepb.AccessOp_ACCESS_OP_CREATE,
		kept:   filepb.AccessOp_ACCESS_OP_WRITE,
		doomed: filepb.AccessOp_ACCESS_OP_DELETE,
	}
	changes := DiffSnapshots(before, after)
	if len(changes) != len(want) {
		t.Fatalf("expected %d changes, got %+v", len(want), changes)
	}
	for _, change := range changes {
		if want[change.Path] != change.Op {
			t.Fatalf("%s: op = %v, want %v", change.Path, change.Op, want[change.Path])
		}
	}
}

func TestSnapshotSkipsIgnoredDirectories(t *testing.T) {
	dir := t.TempDir()
	noisy := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(noisy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noisy, "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := TakeSnapshot(dir, defaultAuditOptions())
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if len(snapshot) != 1 {
		t.Fatalf("expected only the source file, got %+v", snapshot)
	}
}

// A tree over the limit must not produce a snapshot at all: diffing against a
// truncated one invents a deletion for every file that fell off the end.
func TestSnapshotRefusesOversizedTree(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := TakeSnapshot(dir, AuditOptions{Enabled: true, MaxEntries: 3}); err == nil {
		t.Fatal("expected a refusal, got a snapshot")
	}
}

// The refusal itself is an observation: a consumer must be able to tell "no
// record was kept" from "nothing was touched".
func TestAroundReportsAnUnauditedExecution(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)
	audit.Configure(&filepb.WatchConfig{Enabled: true, MaxEntries: 1})

	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ran := false
	if err := audit.Around(context.Background(), dir, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("Around: %v", err)
	}
	if !ran {
		t.Fatal("a refused snapshot must not stop the work")
	}
	if access := c.wait(t, 1)[0]; access.GetError() == "" {
		t.Fatalf("expected the observation to carry the reason, got %+v", access)
	}
}

func TestAroundAttributesShellWritesToTheCall(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	dir := t.TempDir()
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{CallID: "call-7", WorkDir: dir})
	target := filepath.Join(dir, "out.txt")
	if err := audit.Around(ctx, dir, func() error {
		return os.WriteFile(target, []byte("produced by a shell command"), 0o644)
	}); err != nil {
		t.Fatalf("Around: %v", err)
	}

	access := c.wait(t, 1)[0]
	if access.GetPath() != target {
		t.Fatalf("path = %q, want %q", access.GetPath(), target)
	}
	if access.GetOp() != filepb.AccessOp_ACCESS_OP_CREATE {
		t.Fatalf("op = %v, want CREATE", access.GetOp())
	}
	if access.GetSource() != filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT {
		t.Fatalf("source = %v, want SNAPSHOT", access.GetSource())
	}
	if access.GetToolId() != "call-7" {
		t.Fatalf("tool id = %q, want the call that ran the command", access.GetToolId())
	}
}

func TestWriteToolRecordsCreateWriteAndEdit(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	dir := t.TempDir()
	tool := NewWriteTool(dir).WithAudit(audit)
	path := filepath.Join(dir, "note.txt")
	ctx := context.Background()

	if _, err := tool.Execute(ctx, `{"path": "note.txt", "content": "first\n"}`); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := tool.Execute(ctx, `{"path": "note.txt", "content": "second\n"}`); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := tool.Execute(ctx, `{"path": "note.txt", "edits": [{"old_text": "second", "new_text": "third"}]}`); err != nil {
		t.Fatalf("edit: %v", err)
	}

	got := c.wait(t, 3)
	want := []filepb.AccessOp{
		filepb.AccessOp_ACCESS_OP_CREATE,
		filepb.AccessOp_ACCESS_OP_WRITE,
		filepb.AccessOp_ACCESS_OP_EDIT,
	}
	for i, op := range want {
		if got[i].GetOp() != op {
			t.Fatalf("observation %d: op = %v, want %v", i, got[i].GetOp(), op)
		}
		if got[i].GetPath() != path {
			t.Fatalf("observation %d: path = %q", i, got[i].GetPath())
		}
		if got[i].GetDigest() == "" {
			t.Fatalf("observation %d: a write must carry its content digest", i)
		}
	}
	if got[2].GetEdits() != 1 {
		t.Fatalf("edit count = %d, want 1", got[2].GetEdits())
	}
	if got[0].GetDigest() == got[1].GetDigest() {
		t.Fatal("two different contents must not share a digest")
	}
}

func TestReadToolRecordsWhatTheModelReceived(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir).WithAudit(audit)
	if _, err := tool.Execute(context.Background(), `{"path": "data.txt"}`); err != nil {
		t.Fatalf("read: %v", err)
	}

	c.wait(t, 1)
	access := c.byPath(t, path)
	if access.GetOp() != filepb.AccessOp_ACCESS_OP_READ {
		t.Fatalf("op = %v, want READ", access.GetOp())
	}
	if access.GetSource() != filepb.AccessSource_ACCESS_SOURCE_TOOL {
		t.Fatalf("source = %v, want TOOL", access.GetSource())
	}
	if access.GetSize() != 11 {
		t.Fatalf("size = %d, want the file's 11 bytes", access.GetSize())
	}
	if access.GetBytes() == 0 {
		t.Fatal("a read must report how much reached the model")
	}
}

// A read that never touched the filesystem must not appear as one: an embedded
// skill has no path an operator could open.
func TestReadToolDoesNotRecordVirtualReads(t *testing.T) {
	audit := NewFileAudit()
	defer audit.Close()
	c := collect(t, audit)

	tool := NewReadTool(t.TempDir(), staticVirtualReader{"aiscan://skill": "content"}).WithAudit(audit)
	if _, err := tool.Execute(context.Background(), `{"path": "aiscan://skill"}`); err != nil {
		t.Fatalf("read: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.accesses) != 0 {
		t.Fatalf("expected no observation, got %+v", c.accesses)
	}
}

type staticVirtualReader map[string]string

func (r staticVirtualReader) ReadVirtual(path string) (string, bool, error) {
	content, ok := r[path]
	return content, ok, nil
}
