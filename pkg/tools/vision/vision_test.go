package vision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsPositionalPrompt(t *testing.T) {
	path, prompt, err := parseArgs([]string{"/tmp/img.png", "Read", "all", "text"}, "")
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if path != "/tmp/img.png" {
		t.Fatalf("path = %q", path)
	}
	if prompt != "Read all text" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestParseArgsFlagPrompt(t *testing.T) {
	path, prompt, err := parseArgs([]string{"/tmp/img.png", "--prompt", "Describe this image"}, "")
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if path != "/tmp/img.png" {
		t.Fatalf("path = %q", path)
	}
	if prompt != "Describe this image" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestParseArgsRequiresImagePath(t *testing.T) {
	_, _, err := parseArgs([]string{}, "usage text")
	if err == nil {
		t.Fatal("expected error for empty args")
	}
	if !strings.Contains(err.Error(), "image_path") {
		t.Fatalf("error = %q", err)
	}
}

func TestParseArgsRequiresPrompt(t *testing.T) {
	_, _, err := parseArgs([]string{"/tmp/img.png"}, "usage text")
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("error = %q", err)
	}
}

func TestParseArgsRejectsUnknownFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"/tmp/img.png", "--bad", "value"}, "")
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestMimeFromExt(t *testing.T) {
	tests := []struct {
		ext  string
		mime string
		ok   bool
	}{
		{".png", "image/png", true},
		{".PNG", "image/png", true},
		{".jpg", "image/jpeg", true},
		{".jpeg", "image/jpeg", true},
		{".gif", "image/gif", true},
		{".webp", "image/webp", true},
		{".bmp", "image/bmp", true},
		{".svg", "", false},
		{".txt", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		mime, ok := mimeFromExt(tt.ext)
		if ok != tt.ok || mime != tt.mime {
			t.Errorf("mimeFromExt(%q) = (%q, %v), want (%q, %v)", tt.ext, mime, ok, tt.mime, tt.ok)
		}
	}
}

func TestReadImageFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := readImageFile(dir)
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestReadImageFileRejectsOversized(t *testing.T) {
	// Create a file that's just over the limit header — readImageFile reads
	// maxImageSize+1 bytes to detect overflow, so we need a file larger than
	// that to trigger the check. Since maxImageSize is 20 MB we test with
	// a sparse trick: just check the stat-based early rejection.
	dir := t.TempDir()
	path := filepath.Join(dir, "big.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Truncate to maxImageSize + 1 — creates a sparse file instantly.
	if err := f.Truncate(maxImageSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = readImageFile(path)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want 'too large'", err)
	}
}

func TestReadImageFileReadsSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	data := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readImageFile(path)
	if err != nil {
		t.Fatalf("readImageFile() error = %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
}

func TestExecuteWithoutProviderReturnsError(t *testing.T) {
	cmd := New(nil)
	_, err := cmd.Execute(nil, []string{"test.png", "describe"})
	if err == nil {
		t.Fatal("expected error without provider")
	}
	if !strings.Contains(err.Error(), "no LLM provider") {
		t.Fatalf("error = %q", err)
	}
}
