package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClip_Short(t *testing.T) {
	got := Clip("hello", 100)
	if got != "hello" {
		t.Fatalf("expected no change, got %q", got)
	}
}

func TestClip_WhitespaceCollapse(t *testing.T) {
	got := Clip("line1\nline2\n  line3", 100)
	if got != "line1 line2 line3" {
		t.Fatalf("expected collapsed whitespace, got %q", got)
	}
}

func TestClip_Truncation(t *testing.T) {
	got := Clip(strings.Repeat("x", 200), 50)
	if len(got) > 50 {
		t.Fatalf("expected at most 50 bytes, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ... suffix, got %q", got)
	}
}

func TestClip_UTF8(t *testing.T) {
	got := Clip("你好世界再见", 10)
	if !utf8.ValidString(got) {
		t.Fatal("result is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ... suffix, got %q", got)
	}
}

func TestClip_VerySmallMax(t *testing.T) {
	got := Clip("hello world", 3)
	if got != "..." {
		t.Fatalf("expected '...', got %q", got)
	}
}

func TestClipRunes_Short(t *testing.T) {
	got := ClipRunes("hello", 10)
	if got != "hello" {
		t.Fatalf("expected no change, got %q", got)
	}
}

func TestClipRunes_Truncation(t *testing.T) {
	got := ClipRunes("abcdefghij", 5)
	if got != "abcd…" {
		t.Fatalf("expected 'abcd…', got %q", got)
	}
	if utf8.RuneCountInString(got) != 5 {
		t.Fatalf("expected 5 runes, got %d", utf8.RuneCountInString(got))
	}
}

func TestClipRunes_CJK(t *testing.T) {
	got := ClipRunes("你好世界再见朋友", 4)
	if utf8.RuneCountInString(got) != 4 {
		t.Fatalf("expected 4 runes, got %d: %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected … suffix, got %q", got)
	}
}

func TestClipLines_Basic(t *testing.T) {
	text := "line1\nline2\nline3\nline4\nline5"
	lines, hidden := ClipLines(text, 3, 100)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if hidden != 2 {
		t.Fatalf("expected 2 hidden, got %d", hidden)
	}
	if lines[0] != "line1" {
		t.Fatalf("expected 'line1', got %q", lines[0])
	}
}

func TestClipLines_WidthTruncation(t *testing.T) {
	text := "short\n" + strings.Repeat("x", 200)
	lines, _ := ClipLines(text, 10, 50)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if utf8.RuneCountInString(lines[1]) > 51 { // 50 + "…"
		t.Fatalf("line too long: %d runes", utf8.RuneCountInString(lines[1]))
	}
}

func TestClipLines_NoTruncation(t *testing.T) {
	text := "a\nb"
	lines, hidden := ClipLines(text, 10, 100)
	if len(lines) != 2 || hidden != 0 {
		t.Fatalf("expected 2 lines 0 hidden, got %d lines %d hidden", len(lines), hidden)
	}
}

func TestClipLines_NonPositiveLimitsDoNotPanic(t *testing.T) {
	lines, hidden := ClipLines("a\nb", 0, 100)
	if len(lines) != 0 || hidden != 2 {
		t.Fatalf("expected no lines and 2 hidden, got %d lines %d hidden", len(lines), hidden)
	}

	lines, hidden = ClipLines("abc", 1, -1)
	if len(lines) != 1 || hidden != 0 || lines[0] != "…" {
		t.Fatalf("unexpected negative width result: lines=%q hidden=%d", lines, hidden)
	}
}
