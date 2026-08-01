package agent

import (
	"os"
	"path/filepath"
	"testing"

	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func TestDefaultAgentRuntimeDoesNotAdvertiseRunnerFileRPCs(t *testing.T) {
	for _, capability := range DefaultRuntime().Capabilities {
		if capability == "file.list" || capability == "file.mkdir" {
			t.Fatalf("regular agent advertised runner-only capability %q", capability)
		}
	}
}

func TestFileListReturnsStructuredEntries(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	value := fileList(&transport.FileListRequest{TaskId: "list-1", Path: "."}, base)
	if value.err != nil {
		t.Fatal(value.err)
	}
	if value.result.Path != "." || len(value.result.Entries) != 2 {
		t.Fatalf("result = %+v", value.result)
	}
	byName := map[string]*transport.FileEntry{}
	for _, entry := range value.result.Entries {
		byName[entry.Name] = entry
	}
	if byName["note.txt"].IsDirectory || byName["note.txt"].Size != 4 {
		t.Fatalf("file entry = %+v", byName["note.txt"])
	}
	if !byName["nested"].IsDirectory {
		t.Fatalf("directory entry = %+v", byName["nested"])
	}
}

func TestNativeFileRPCsResolveRelativeToRuntimeWorkdir(t *testing.T) {
	base := t.TempDir()
	if value := fileMkdir(&transport.FileMkdirRequest{TaskId: "mkdir-1", Path: "nested"}, base); value.err != nil {
		t.Fatal(value.err)
	}
	path := filepath.Join("nested", "proof.txt")
	if value := fileWrite(&transport.FileWriteRequest{TaskId: "write-1", Path: path, Data: []byte("hello")}, base); value.err != nil {
		t.Fatal(value.err)
	}
	value := fileRead(&transport.FileReadRequest{TaskId: "read-1", Path: path}, base)
	if value.err != nil || string(value.result.Data) != "hello" {
		t.Fatalf("read data = %q, err = %v", value.result.Data, value.err)
	}
}
