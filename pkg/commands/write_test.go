package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
