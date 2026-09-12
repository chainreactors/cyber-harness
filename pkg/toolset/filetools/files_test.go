package filetools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/extensions/toolgroup"
	"github.com/chainreactors/aiscan/pkg/files"
	"github.com/chainreactors/aiscan/pkg/toolset/filetools"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

func fileSet(t *testing.T, cfg files.Config) (*registry.Registry, *extension.Set) {
	t.Helper()
	r := registry.New()
	fs, err := files.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := filetools.Tools(fs)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := toolgroup.New(r, definitions...)
	if err != nil {
		t.Fatal(err)
	}
	s, err := extension.New(
		extension.Entry{ID: "registry", Extension: r},
		extension.Entry{ID: "fs", Extension: fs},
		extension.Entry{ID: "file-tools", DependsOn: []string{"registry", "fs"}, Extension: instance},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return r, s
}

func args(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFileExtensionRoundTripAndOwnership(t *testing.T) {
	dir := t.TempDir()
	r, set := fileSet(t, files.Config{Directory: dir})
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("construction published tools")
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "你好\nsecond", ""} {
		_, err := r.ExecuteTool(t.Context(), "write", args(t, map[string]string{"path": "note.txt", "content": text}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := r.ExecuteTool(t.Context(), "read", `{"path":"note.txt"}`)
		if err != nil || tool.ResultText(result) != text {
			t.Fatalf("read back: %v, %v", result, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
		if err != nil || string(data) != text {
			t.Fatalf("actual filesystem contents: %q, %v", data, err)
		}
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("file definitions survived instance close")
	}
	if _, err := r.ExecuteTool(t.Context(), "read", `{"path":"note.txt"}`); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("closed tool remained callable: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "note.txt" {
		t.Fatalf("temporary file leaked: %v, %v", entries, err)
	}
	// A closed instance releases its root handle, including on Windows.
	if err := os.Rename(dir, dir+"-closed"); err != nil {
		t.Fatalf("root handle retained after close: %v", err)
	}
	if err := os.Rename(dir+"-closed", dir); err != nil {
		t.Fatal(err)
	}
}

func TestConstructionDoesNotOpenOrCreateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	r, set := fileSet(t, files.Config{Directory: dir})
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor touched directory: %v", err)
	}
	if err := set.Load(t.Context()); err == nil {
		t.Fatal("loaded missing root")
	}
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("partial startup published tools")
	}
	if _, err := r.ExecuteTool(t.Context(), "read", "{}"); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("startup rollback did not close newly loaded dependency: %v", err)
	}
}

// Each Set has real ownership; the test closes borrower Sets before their
// shared resource Set. No extension is handed to two Sets.
func ownedSet(t *testing.T, entries ...extension.Entry) *extension.Set {
	t.Helper()
	s, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func fileResources(t *testing.T) (*registry.Registry, *files.FS) {
	t.Helper()
	r := registry.New()
	fs, err := files.New(files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	resources := ownedSet(t, extension.Entry{ID: "registry", Extension: r}, extension.Entry{ID: "fs", Extension: fs})
	if err := resources.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r, fs
}

func fileGroup(t *testing.T, r *registry.Registry, fs *files.FS) *extension.Set {
	t.Helper()
	definitions, err := filetools.Tools(fs)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := toolgroup.New(r, definitions...)
	if err != nil {
		t.Fatal(err)
	}
	return ownedSet(t, extension.Entry{ID: "file-tools", Extension: instance})
}

func TestFailedRegistrationDoesNotRevokeExistingFiles(t *testing.T) {
	r, fs := fileResources(t)
	one, two := fileGroup(t, r, fs), fileGroup(t, r, fs)
	if err := one.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := two.Load(t.Context()); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("tool collision: %v", err)
	}
	if err := two.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"original.txt","content":"retained"}`); err != nil {
		t.Fatalf("failed registration revoked original tools: %v", err)
	}
}

func TestClosingToolsKeepsBorrowedFilesystemUsable(t *testing.T) {
	r, fs := fileResources(t)
	group := fileGroup(t, r, fs)
	if err := group.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"file","content":"original"}`); err != nil {
		t.Fatal(err)
	}
	if err := group.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("tools survived group Close")
	}
	if _, err := r.ExecuteTool(t.Context(), "read", `{"path":"file"}`); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("closed tool callable: %v", err)
	}
	data, err := fs.Read(t.Context(), "file")
	if err != nil || string(data) != "original" {
		t.Fatalf("group closed borrowed FS: %q, %v", data, err)
	}
	if err := fs.Write(t.Context(), "file", []byte("direct")); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedWritesLeaveDestinationAndNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	r, set := fileSet(t, files.Config{Directory: dir, MaxBytes: 4})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"path":"note.txt"}`,
		`{"path":"note.txt","content":null}`,
		`{"path":"note.txt","content":"oversized"}`,
		`{"path":"../escape.txt","content":"no"}`,
		`{"path":".","content":"no"}`,
		`not-json`,
	} {
		if _, err := r.ExecuteTool(t.Context(), "write", input); err == nil {
			t.Errorf("accepted invalid write: %s", input)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ExecuteTool(ctx, "write", `{"path":"note.txt","content":"new"}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("rejected write modified destination: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("rejected write left temporary files: %v, %v", entries, err)
	}
}

func TestReadLimitsAndReadOnlyDiscovery(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{"large": []byte("12345"), "binary": {0xff}, "valid": []byte("a界")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, set := fileSet(t, files.Config{Directory: dir, ReadOnly: true, MaxBytes: 4})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if defs := r.ToolDefinitions(); len(defs) != 3 || defs[0].Name != "read" {
		t.Fatalf("read-only discovery: %v", defs)
	}
	if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"valid","content":"x"}`); !errors.Is(err, registry.ErrUnknown) {
		t.Fatalf("read-only write: %v", err)
	}
	for _, name := range []string{"large", "binary", "..", "missing"} {
		if _, err := r.ExecuteTool(t.Context(), "read", args(t, map[string]string{"path": name})); err == nil {
			t.Errorf("accepted invalid read: %s", name)
		}
	}
	if result, err := r.ExecuteTool(t.Context(), "read", `{"path":"valid"}`); err != nil || tool.ResultText(result) != "a界" {
		t.Fatalf("UTF-8 byte limit: %v, %v", result, err)
	}
}

func TestSeparateSetsKeepFileRootsIsolated(t *testing.T) {
	first, one := fileSet(t, files.Config{Directory: t.TempDir()})
	second, two := fileSet(t, files.Config{Directory: t.TempDir()})
	for _, set := range []*extension.Set{one, two} {
		if err := set.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := first.ExecuteTool(t.Context(), "write", `{"path":"only-first","content":"one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ExecuteTool(t.Context(), "read", `{"path":"only-first"}`); err == nil {
		t.Fatal("file state leaked across sets")
	}
	if err := one.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ExecuteTool(t.Context(), "write", `{"path":"still-active","content":"two"}`); err != nil {
		t.Fatalf("closing first set stopped second: %v", err)
	}
}

func TestSymlinkCannotLeaveConfiguredRoot(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "note.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Windows symlink permission unavailable: %v", err)
		}
		t.Fatal(err)
	}
	r, set := fileSet(t, files.Config{Directory: dir})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(t.Context(), "read", `{"path":"link/note.txt"}`); err == nil {
		t.Fatal("read escaped configured root")
	}
	if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"link/note.txt","content":"changed"}`); err == nil {
		t.Fatal("write escaped configured root")
	}
	data, err := os.ReadFile(filepath.Join(outside, "note.txt"))
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}

func TestProductionDependenciesStayIndependent(t *testing.T) {
	const prefix = "github.com/chainreactors/aiscan/"
	cmd := exec.CommandContext(t.Context(), "go", "list", "-mod=readonly", "-deps", prefix+"pkg/toolset/registry", prefix+"pkg/toolset/filetools", prefix+"pkg/extensions/toolgroup")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency inspection: %v\n%s", err, output)
	}
	allowed := []string{"aop", "core/capability", "core/eventbus", "core/extension", "core/tool", "pkg/toolset/registry", "pkg/toolset/filetools", "pkg/extensions/toolgroup", "pkg/files"}
	for _, dep := range strings.Fields(string(output)) {
		if !strings.HasPrefix(dep, prefix) {
			continue
		}
		local := strings.TrimPrefix(dep, prefix)
		ok := false
		for _, path := range allowed {
			if local == path || path == "aop" && strings.HasPrefix(local, "aop/") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("unexpected production dependency: %s", dep)
		}
	}
}
