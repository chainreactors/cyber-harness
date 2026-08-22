package traffic

import (
	"io"
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
