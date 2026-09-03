package traffic

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBodySinkStreamsAndHydrates(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewBodySink(dir, "response", 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(sink, strings.NewReader("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	ref, err := sink.Close(true)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size != 8 || string(sink.Preview()) != "abcd" || !ref.Complete {
		t.Fatalf("unexpected ref: %+v preview=%q", ref, sink.Preview())
	}
	body, err := ReadBody(&ref)
	if err != nil || string(body) != "abcdefgh" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestBodySinkLimitRetainsPrefix(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewBodySinkWithLimit(dir, "response", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := io.Copy(sink, strings.NewReader("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("copy n=%d err=%v, want all 8 bytes consumed", n, err)
	}
	ref, err := sink.Close(true)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size != 8 || !ref.Truncated || !ref.Complete {
		t.Fatalf("ref=%+v, want complete truncated capture", ref)
	}
	body, err := ReadBody(&ref)
	if err != nil || string(body) != "abcd" {
		t.Fatalf("stored body=%q err=%v, want prefix", body, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "response")); err != nil || info.Size() != 4 {
		t.Fatalf("stored file stat=%v info=%v, want 4 bytes", err, info)
	}
}
