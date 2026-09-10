package console

import (
	tuiConsole "github.com/chainreactors/tui/console"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"strings"
	"testing"
)

func testReadlineBridge(t *testing.T) (*readlineConsoleBridge, *syncedBuffer) {
	t.Helper()
	raw := &syncedBuffer{}
	shell := tuiConsole.NewWithTerminal("test", rlterm.Stream(strings.NewReader(""), raw, raw, rlterm.NewControl(true, 80, 24))).Shell()
	b := newReadlineConsoleBridge(shell, raw)
	b.SetActive(true)
	return b, raw
}
func TestReadlineConsoleBridgeCommitsCompleteLines(t *testing.T) {
	b, raw := testReadlineBridge(t)
	_, _ = b.Write([]byte("hello"))
	if strings.Contains(raw.String(), "hello") {
		t.Fatal("partial line committed")
	}
	_, _ = b.Write([]byte(" world\nnext"))
	if !strings.Contains(raw.String(), "hello world") {
		t.Fatalf("output=%q", raw.String())
	}
	if strings.Contains(raw.String(), "next") {
		t.Fatal("remainder committed")
	}
	_, _ = b.Write([]byte("\n"))
	if !strings.Contains(raw.String(), "next") {
		t.Fatal("remainder missing")
	}
}
func TestReadlineConsoleBridgePreservesMultilineBatches(t *testing.T) {
	b, raw := testReadlineBridge(t)
	_, _ = b.Write([]byte("one\r\ntwo\nthree"))
	text := stripANSI(raw.String())
	if !strings.Contains(text, "one") || !strings.Contains(text, "two") || strings.Contains(text, "three") {
		t.Fatalf("batch=%q", text)
	}
	_, _ = b.Write([]byte("\n"))
	if !strings.Contains(raw.String(), "three") {
		t.Fatal("remainder missing")
	}
}
func TestReadlineConsoleBridgeWritesDirectlyWhenInactive(t *testing.T) {
	b, raw := testReadlineBridge(t)
	_, _ = b.Write([]byte("pending"))
	b.SetActive(false)
	_, _ = b.Write([]byte(" direct\n"))
	if got := raw.String(); got != "pending direct\n" {
		t.Fatalf("output=%q", got)
	}
}
func TestReadlineConsoleBridgeRetainsStatusUntilPromptReady(t *testing.T) {
	b, raw := testReadlineBridge(t)
	_ = b.Status()
	b.UpdateStatus("thinking")
	if raw.String() != "" {
		t.Fatal("redrew before prompt ready")
	}
	b.SetReady(true)
	if b.Status() != "thinking" {
		t.Fatal("status lost")
	}
	b.SetReady(false)
	b.SetActive(false)
	b.UpdateStatus("latest")
	if b.Status() != "latest" {
		t.Fatal("inactive status lost")
	}
}
