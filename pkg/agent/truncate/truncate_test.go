package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHead_NoTruncation(t *testing.T) {
	content := "line1\nline2\nline3"
	r := Head(content, Options{})
	if r.Truncated {
		t.Fatal("expected no truncation")
	}
	if r.Content != content {
		t.Fatalf("content mismatch: %q", r.Content)
	}
	if r.TotalLines != 3 || r.OutputLines != 3 {
		t.Fatalf("line counts: total=%d output=%d", r.TotalLines, r.OutputLines)
	}
}

func TestHead_LineLimitHit(t *testing.T) {
	lines := make([]string, 2500)
	for i := range lines {
		lines[i] = "x"
	}
	content := strings.Join(lines, "\n")
	r := Head(content, Options{MaxLines: 10, MaxBytes: 1 << 20})
	if !r.Truncated || r.TruncatedBy != ByLines {
		t.Fatalf("expected truncation by lines, got truncated=%v by=%s", r.Truncated, r.TruncatedBy)
	}
	if r.OutputLines != 10 {
		t.Fatalf("expected 10 output lines, got %d", r.OutputLines)
	}
	if r.TotalLines != 2500 {
		t.Fatalf("expected 2500 total lines, got %d", r.TotalLines)
	}
}

func TestHead_ByteLimitHit(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("a", 1000)
	}
	content := strings.Join(lines, "\n")
	r := Head(content, Options{MaxBytes: 3500})
	if !r.Truncated || r.TruncatedBy != ByBytes {
		t.Fatalf("expected truncation by bytes, got truncated=%v by=%s", r.Truncated, r.TruncatedBy)
	}
	if r.OutputLines != 3 {
		t.Fatalf("expected 3 output lines, got %d", r.OutputLines)
	}
}

func TestHead_FirstLineExceedsLimit(t *testing.T) {
	content := strings.Repeat("x", 60000)
	r := Head(content, Options{MaxBytes: 50 * 1024})
	if !r.Truncated || !r.FirstLineExceedsLimit {
		t.Fatalf("expected FirstLineExceedsLimit, got %+v", r)
	}
	if r.Content != "" {
		t.Fatalf("expected empty content, got %d bytes", len(r.Content))
	}
}

func TestHead_Empty(t *testing.T) {
	r := Head("", Options{})
	if r.Truncated {
		t.Fatal("expected no truncation on empty")
	}
	if r.TotalLines != 0 {
		t.Fatalf("expected 0 total lines, got %d", r.TotalLines)
	}
}

func TestHead_UTF8(t *testing.T) {
	content := "你好世界\n这是测试"
	r := Head(content, Options{MaxBytes: 20})
	if !r.Truncated {
		t.Fatal("expected truncation")
	}
	if r.OutputLines != 1 {
		t.Fatalf("expected 1 output line, got %d", r.OutputLines)
	}
	if r.Content != "你好世界" {
		t.Fatalf("unexpected content: %q", r.Content)
	}
}

func TestTail_NoTruncation(t *testing.T) {
	content := "line1\nline2"
	r := Tail(content, Options{})
	if r.Truncated {
		t.Fatal("expected no truncation")
	}
	if r.Content != content {
		t.Fatalf("content mismatch: %q", r.Content)
	}
}

func TestTail_LineLimitHit(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "x"
	}
	content := strings.Join(lines, "\n")
	r := Tail(content, Options{MaxLines: 10, MaxBytes: 1 << 20})
	if !r.Truncated || r.TruncatedBy != ByLines {
		t.Fatalf("expected truncation by lines, got truncated=%v by=%s", r.Truncated, r.TruncatedBy)
	}
	if r.OutputLines != 10 {
		t.Fatalf("expected 10 output lines, got %d", r.OutputLines)
	}
	if !strings.HasPrefix(r.Content, "x") {
		t.Fatalf("tail should contain last lines")
	}
}

func TestTail_ByteLimitHit(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("b", 1000)
	}
	content := strings.Join(lines, "\n")
	r := Tail(content, Options{MaxBytes: 3500})
	if !r.Truncated || r.TruncatedBy != ByBytes {
		t.Fatalf("expected truncation by bytes, got truncated=%v by=%s", r.Truncated, r.TruncatedBy)
	}
}

func TestTail_PartialLine(t *testing.T) {
	content := strings.Repeat("z", 60000)
	r := Tail(content, Options{MaxBytes: 1000})
	if !r.Truncated {
		t.Fatal("expected truncation")
	}
	if len(r.Content) > 1000 {
		t.Fatalf("expected at most 1000 bytes, got %d", len(r.Content))
	}
	if !strings.HasSuffix(content, r.Content) {
		t.Fatal("tail content should be a suffix of original")
	}
}

func TestTail_UTF8Boundary(t *testing.T) {
	// 3 bytes per CJK character
	content := strings.Repeat("中", 100)
	r := Tail(content, Options{MaxBytes: 10})
	if !r.Truncated {
		t.Fatal("expected truncation")
	}
	// must be valid UTF-8
	for i := 0; i < len(r.Content); {
		_, size := utf8.DecodeRuneInString(r.Content[i:])
		if size == 0 {
			t.Fatal("invalid UTF-8 in result")
		}
		i += size
	}
}

func TestLine_NoTruncation(t *testing.T) {
	text, trunc := Line("short", 100)
	if trunc || text != "short" {
		t.Fatalf("unexpected: %q trunc=%v", text, trunc)
	}
}

func TestLine_Truncation(t *testing.T) {
	text, trunc := Line("abcdefghij", 5)
	if !trunc {
		t.Fatal("expected truncation")
	}
	if text != "abcde... [truncated]" {
		t.Fatalf("unexpected: %q", text)
	}
}

func TestLine_NonPositiveLimitDoesNotPanic(t *testing.T) {
	text, trunc := Line("abc", 0)
	if !trunc || text != "... [truncated]" {
		t.Fatalf("unexpected: %q trunc=%v", text, trunc)
	}

	text, trunc = Line("", -1)
	if trunc || text != "" {
		t.Fatalf("unexpected empty result: %q trunc=%v", text, trunc)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int
		want  string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{51200, "50.0KB"},
		{1048576, "1.0MB"},
		{10485760, "10.0MB"},
	}
	for _, tt := range tests {
		got := FormatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
