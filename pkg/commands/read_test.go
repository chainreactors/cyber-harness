package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !strings.Contains(out, "line1") {
		t.Fatalf("expected line1 in output, got: %s", out)
	}
	if !strings.Contains(out, "line2") {
		t.Fatalf("expected line2 in output, got: %s", out)
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
	if !strings.Contains(out, "line number 50") {
		t.Fatalf("expected line 50 at start, got: %s", out)
	}
	if !strings.Contains(out, "line number 59") {
		t.Fatalf("expected line 59 at end, got: %s", out)
	}
	if strings.Contains(out, "line number 60") {
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

func TestReadImageFileByMagicBytes(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name   string
		header []byte
		mime   string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{"gif", []byte("GIF89a"), "image/gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
	}

	for _, tt := range tests {
		path := filepath.Join(dir, tt.name+".dat")
		os.WriteFile(path, tt.header, 0644)

		tool := NewReadTool(dir)
		res, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": "%s.dat"}`, tt.name))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		if !res.HasImages() {
			t.Fatalf("%s: expected image content", tt.name)
		}
		if !strings.Contains(res.Text(), tt.mime) {
			t.Fatalf("%s: expected mime %s in text, got: %s", tt.name, tt.mime, res.Text())
		}
	}
}

func TestReadNonImageBinaryNotDetectedAsImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03}, 0644)

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), `{"path": "data.bin"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasImages() {
		t.Fatal("non-image binary should not be detected as image")
	}
}
