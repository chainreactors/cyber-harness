package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func newTestSplitTerminal(cols, rows, inputRows int) (*SplitTerminal, *bytes.Buffer) {
	var buf bytes.Buffer
	st := &SplitTerminal{
		raw:         &buf,
		cols:        cols,
		rows:        rows,
		active:      true,
		outRow:      1,
		outCol:      1,
		inputCurCol: 1,
	}
	st.applyInputRows(inputRows)
	st.outputW = &splitOutputWriter{st: st}
	st.inputW = &splitInputWriter{st: st, raw: &buf}
	return st, &buf
}

func TestSplitInputWriterLocalizesClearScreenBelow(t *testing.T) {
	st, buf := newTestSplitTerminal(40, 12, 4)

	if _, err := st.InputWriter().Write([]byte("abc\x1b[J")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\x1b[J") || strings.Contains(got, "\x1b[0J") {
		t.Fatalf("input writer leaked clear-screen sequence: %q", got)
	}
	if strings.Contains(got, fmt.Sprintf("\x1b[%d;1H\x1b[2K", st.statusRow)) {
		t.Fatalf("input writer cleared status row: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("\x1b[%d;1H\x1b[2K", st.inputRow+1)) {
		t.Fatalf("input writer did not clear below inside input area: %q", got)
	}
	if !strings.Contains(got, "abc") {
		t.Fatalf("input writer dropped printable text: %q", got)
	}
}

func TestSplitInputWriterClampsCursorUp(t *testing.T) {
	st, buf := newTestSplitTerminal(40, 12, 4)

	if _, err := st.InputWriter().Write([]byte("\x1b[5Ahi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\x1b[5A") {
		t.Fatalf("input writer leaked relative cursor-up: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("\x1b[%d;1H", st.inputRow)) {
		t.Fatalf("input writer did not clamp to input top: %q", got)
	}
}

func TestSplitInputWriterDoesNotEmitLineFeed(t *testing.T) {
	st, buf := newTestSplitTerminal(40, 12, 4)

	if _, err := st.InputWriter().Write([]byte("a\nb")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\n") {
		t.Fatalf("input writer emitted raw newline: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("\x1b[%d;1H", st.inputRow+1)) {
		t.Fatalf("input writer did not move newline inside input area: %q", got)
	}
}

func TestSplitInputWriterReadlineCompletionRefreshReturnsToPrompt(t *testing.T) {
	st, _ := newTestSplitTerminal(80, 24, 8)

	refresh := "" +
		"\x1b[?25l" + // hide cursor
		"\x1b[80D" +
		"aiscan ❯ /aiscan\x1b[0K" +
		"\r\n\x1b[0J" +
		"commands\x1b[0K\r\r\n" +
		"/aiscan -- Use this skill when the agent needs to understand aiscan\x1b[0K" +
		"\x1b[80D\x1b[1A\x1b[1A" +
		"\x1b[80D\x1b[9C" +
		"\x1b[80D\x1b[16C" +
		"\x1b[?25h"

	if _, err := st.InputWriter().Write([]byte(refresh)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if st.inputCurRow != 0 {
		t.Fatalf("cursor row after refresh = %d, want prompt row", st.inputCurRow)
	}

	if _, err := st.InputWriter().Write([]byte(refresh)); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if st.inputCurRow != 0 {
		t.Fatalf("cursor row after second refresh = %d, want prompt row", st.inputCurRow)
	}
}

func TestSplitInputWriterExactWidthHelperLineDoesNotAdvance(t *testing.T) {
	st, _ := newTestSplitTerminal(20, 12, 4)

	seq := "\r\n\x1b[0J" +
		strings.Repeat("x", 20) +
		"\x1b[0K\x1b[20D\x1b[1A"

	if _, err := st.InputWriter().Write([]byte(seq)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if st.inputCurRow != 0 {
		t.Fatalf("cursor row after exact-width helper = %d, want prompt row", st.inputCurRow)
	}
}

func TestSplitInputWriterWrapsOnNextPrintableAfterFullColumn(t *testing.T) {
	st, _ := newTestSplitTerminal(5, 12, 4)

	if _, err := st.InputWriter().Write([]byte("abcdeZ")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if st.inputCurRow != 1 || st.inputCurCol != 2 {
		t.Fatalf("cursor = row %d col %d, want row 1 col 2", st.inputCurRow, st.inputCurCol)
	}
}

func TestSplitOutputWriterPinsScrollRegion(t *testing.T) {
	st, buf := newTestSplitTerminal(40, 12, 4)

	if _, err := st.OutputWriter().Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, fmt.Sprintf("\x1b[1;%dr", st.scrollEnd)) {
		t.Fatalf("output writer did not set scroll region: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("\x1b[%d;1H", st.inputRow)) {
		t.Fatalf("output writer did not restore input cursor explicitly: %q", got)
	}
}

func TestSplitOutputWriterLocalizesControlSequences(t *testing.T) {
	st, buf := newTestSplitTerminal(40, 12, 4)

	if _, err := st.OutputWriter().Write([]byte("top\x1b[99;1Hbad\x1b[2J")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\x1b[99;1H") || strings.Contains(got, "\x1b[2J") {
		t.Fatalf("output writer leaked pane-breaking sequence: %q", got)
	}
	if strings.Contains(got, fmt.Sprintf("\x1b[%d;1H\x1b[2K", st.inputRow)) {
		t.Fatalf("output writer cleared input row: %q", got)
	}
	if !strings.Contains(got, "top") || !strings.Contains(got, "bad") {
		t.Fatalf("output writer dropped printable output: %q", got)
	}
}

func TestSplitOutputWriterExactWidthNewlineDoesNotSkipRow(t *testing.T) {
	st, _ := newTestSplitTerminal(5, 12, 4)

	if _, err := st.OutputWriter().Write([]byte("abcde\nz")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if st.outRow != 2 || st.outCol != 2 {
		t.Fatalf("cursor = row %d col %d, want row 2 col 2", st.outRow, st.outCol)
	}
}
