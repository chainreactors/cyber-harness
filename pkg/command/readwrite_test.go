package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Read Tool Tests ---

func TestReadSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": %q}`, "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Text()
	if !strings.Contains(out, "1\tline1") {
		t.Fatalf("expected line-numbered output, got: %s", out)
	}
	if !strings.Contains(out, "2\tline2") {
		t.Fatalf("expected line 2, got: %s", out)
	}
}

func TestReadWithOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	var content strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&content, "line number %d\n", i)
	}
	os.WriteFile(path, []byte(content.String()), 0644)

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), `{"path": "test.txt", "offset": 50, "limit": 10}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Text()
	if !strings.Contains(out, "50\tline number 50") {
		t.Fatalf("expected line 50 at start, got: %s", out)
	}
	if !strings.Contains(out, "59\tline number 59") {
		t.Fatalf("expected line 59 at end, got: %s", out)
	}
	if strings.Contains(out, "60\t") {
		t.Fatalf("should not contain line 60")
	}
	if !strings.Contains(out, "next: read with offset=60") {
		t.Fatalf("expected continuation hint, got: %s", out)
	}
}

func TestReadLargeFileDoesNotOOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")

	// Create a file with 5000 lines — should be truncated by line limit
	f, _ := os.Create(path)
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(f, "line %d: some content here\n", i)
	}
	f.Close()

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), `{"path": "big.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Text()
	// Should stop at default limit (2000 lines) and provide continuation hint
	if !strings.Contains(out, "of 5000 total") {
		t.Fatalf("expected total line count in output, got: %s", out[len(out)-200:])
	}
	if !strings.Contains(out, "next: read with offset=") {
		t.Fatalf("expected continuation hint, got: %s", out[len(out)-200:])
	}
}

func TestReadBinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}, 0644)

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), `{"path": "binary.bin"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "[binary file") {
		t.Fatalf("expected binary file detection, got: %s", res.Text())
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := NewReadTool(t.TempDir())
	_, err := tool.Execute(context.Background(), `{"path": "nonexistent.txt"}`)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadDirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	tool := NewReadTool(dir)
	_, err := tool.Execute(context.Background(), `{"path": "subdir"}`)
	if err == nil {
		t.Fatal("expected error for directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory error, got: %v", err)
	}
}

// --- Write Tool Tests ---

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	res, err := tool.Execute(context.Background(), `{"path": "new.txt", "content": "hello world\n"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "wrote") {
		t.Fatalf("expected write confirmation, got: %s", res.Text())
	}

	data, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(data) != "hello world\n" {
		t.Fatalf("file content mismatch: %q", string(data))
	}
}

func TestWriteCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	_, err := tool.Execute(context.Background(), `{"path": "a/b/c/deep.txt", "content": "deep"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a", "b", "c", "deep.txt"))
	if string(data) != "deep" {
		t.Fatalf("deep file content mismatch: %q", string(data))
	}
}

func TestEditSingleReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("func hello() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "fmt.Println(\"hello\")", "new_text": "fmt.Println(\"world\")"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "edited") {
		t.Fatalf("expected edit confirmation, got: %s", res.Text())
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `Println("world")`) {
		t.Fatalf("edit did not apply: %s", string(data))
	}
	if strings.Contains(string(data), `Println("hello")`) {
		t.Fatalf("old text still present: %s", string(data))
	}
}

func TestEditMultipleEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("aaa\nbbb\nccc\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "aaa", "new_text": "AAA"}, {"old_text": "ccc", "new_text": "CCC"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "2 edit(s)") {
		t.Fatalf("expected 2 edits confirmation, got: %s", res.Text())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if content != "AAA\nbbb\nCCC\n" {
		t.Fatalf("multi-edit result mismatch: %q", content)
	}
}

func TestEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("func hello() {}\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "does not exist", "new_text": "replacement"}]}`)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for old_text not found")
	}
	if !strings.Contains(res.Text(), "not found") {
		t.Fatalf("expected not found message, got: %s", res.Text())
	}
}

func TestEditAmbiguousWithoutReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("x = 1\nx = 1\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "x = 1", "new_text": "x = 2"}]}`)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for ambiguous match")
	}
	if !strings.Contains(res.Text(), "2 locations") {
		t.Fatalf("expected ambiguity message, got: %s", res.Text())
	}
}

func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("x = 1\ny = 2\nx = 1\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "x = 1", "new_text": "x = 99", "replace_all": true}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "x = 1") {
		t.Fatalf("replace_all did not replace all occurrences: %s", content)
	}
	if strings.Count(content, "x = 99") != 2 {
		t.Fatalf("expected 2 replacements, got: %s", content)
	}
	if !strings.Contains(res.Text(), "2 occurrences") {
		t.Fatalf("expected occurrence count in summary, got: %s", res.Text())
	}
}

func TestEditOverlapDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("abcdef\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "abcd", "new_text": "ABCD"}, {"old_text": "cdef", "new_text": "CDEF"}]}`)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for overlapping edits")
	}
	if !strings.Contains(res.Text(), "overlap") {
		t.Fatalf("expected overlap message, got: %s", res.Text())
	}
}

func TestEditReportsLineNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("line1\nline2\nTARGET\nline4\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "TARGET", "new_text": "REPLACED"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "line 3") {
		t.Fatalf("expected edit at line 3, got: %s", res.Text())
	}
}

func TestEditEmptyOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("content\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "", "new_text": "something"}]}`)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for empty old_text")
	}
}

func TestEditNoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("same\n"), 0644)

	tool := NewWriteTool(dir)
	res, err := tool.Execute(context.Background(),
		`{"path": "code.go", "edits": [{"old_text": "same", "new_text": "same"}]}`)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true when old_text == new_text")
	}
}

// --- Glob Tool Tests ---

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
