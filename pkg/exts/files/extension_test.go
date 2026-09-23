package files_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"

	"github.com/chainreactors/cyber/tools/files"
)

func TestReadRuntimeDocument(t *testing.T) {
	r, set := fileSet(t, files.Config{Directory: t.TempDir(), Mounts: map[string]fs.FS{
		skillsext.RuntimeDocsURI: skillsext.RuntimeDocsFS(),
	}})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := r.ExecuteTool(t.Context(), "read", `{"path":"cyber://skills/runtime/tmux.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(skillsext.RuntimeDocsFS(), "tmux.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := coretool.ResultText(result); got != string(want) {
		t.Fatalf("runtime document content differs: %q", got)
	}
}

func fileSet(t *testing.T, cfg files.Config) (coretool.Executor, *extension.Set) {
	t.Helper()
	registry := coretool.NewToolRegistry()
	f := fileext.New(cfg)
	set, err := extension.New(
		hosttest.Capabilities(),
		registry,
		f,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return registry, set
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
		if err != nil || coretool.ResultText(result) != text {
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
	if _, err := r.ExecuteTool(t.Context(), "read", `{"path":"note.txt"}`); !errors.Is(err, coretool.ErrUnavailable) {
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

// The extension owns the lifetime; the file access it builds must not offer a
// second one for a consumer to drive.
func TestFilesDoesNotExposeLifecycle(t *testing.T) {
	filesystem := reflect.TypeFor[*files.Files]()
	for _, method := range []string{"Open", "Close"} {
		if _, exists := filesystem.MethodByName(method); exists {
			t.Errorf("file access exposes lifecycle method %s", method)
		}
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
	if _, err := r.ExecuteTool(t.Context(), "read", "{}"); !errors.Is(err, coretool.ErrUnavailable) {
		t.Fatalf("startup rollback did not close newly loaded dependency: %v", err)
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
	if _, err := r.ExecuteTool(t.Context(), "write", `{"path":"valid","content":"x"}`); !errors.Is(err, coretool.ErrUnknown) {
		t.Fatalf("read-only write: %v", err)
	}
	for _, name := range []string{"large", "binary", "..", "missing"} {
		if _, err := r.ExecuteTool(t.Context(), "read", args(t, map[string]string{"path": name})); err == nil {
			t.Errorf("accepted invalid read: %s", name)
		}
	}
	if result, err := r.ExecuteTool(t.Context(), "read", `{"path":"valid"}`); err != nil || coretool.ResultText(result) != "a界" {
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
	const prefix = "github.com/chainreactors/cyber/"
	cmd := exec.CommandContext(t.Context(), "go", "list", "-mod=readonly", "-deps", prefix+"pkg/exts/files")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency inspection: %v\n%s", err, output)
	}
	allowed := []string{
		"aop", "core/eventbus", "core/extension", "core/hooks", "core/operation", "core/registry", "core/resource",
		"core/tool", "core/tool/hooks", "core/proc", "core/types", "pkg/exts/files", "tools/files",
	}
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
