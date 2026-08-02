package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	coretool "github.com/chainreactors/aiscan/core/tool"
)

func TestListToolReturnsStructuredDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := NewListTool(dir).Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listing ListResult
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &listing); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, coretool.ResultText(result))
	}
	if listing.Path != "." || len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v", listing)
	}
	byName := map[string]ListEntry{}
	for _, entry := range listing.Entries {
		byName[entry.Name] = entry
	}
	if !byName["nested"].IsDirectory {
		t.Fatalf("directory entry = %+v", byName["nested"])
	}
	if byName["note.txt"].IsDirectory || byName["note.txt"].Size != 4 {
		t.Fatalf("file entry = %+v", byName["note.txt"])
	}
}

func TestListToolUsesInvocationWorkdir(t *testing.T) {
	defaultDir := t.TempDir()
	invocationDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(invocationDir, "proof.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{WorkDir: invocationDir})

	result, err := NewListTool(defaultDir).Execute(ctx, `{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	var listing ListResult
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "proof.txt" {
		t.Fatalf("listing = %+v", listing)
	}
}
