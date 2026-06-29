package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("txt"), 0644)

	tool := NewGlobTool(dir)
	res, err := tool.Execute(context.Background(), `{"pattern": "*.go"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Text()
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Fatalf("expected go files, got: %s", out)
	}
	if strings.Contains(out, "c.txt") {
		t.Fatalf("should not contain txt files, got: %s", out)
	}
}

func TestGlobRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0755)
	os.WriteFile(filepath.Join(dir, "root.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "src", "pkg", "lib.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "src", "pkg", "lib.txt"), []byte(""), 0644)

	tool := NewGlobTool(dir)
	res, err := tool.Execute(context.Background(), `{"pattern": "**/*.go"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Text()
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected src/main.go in recursive match, got: %s", out)
	}
	if !strings.Contains(out, filepath.Join("src", "pkg", "lib.go")) {
		t.Fatalf("expected src/pkg/lib.go in recursive match, got: %s", out)
	}
	if strings.Contains(out, "lib.txt") {
		t.Fatalf("should not contain txt files, got: %s", out)
	}
}
