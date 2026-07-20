package cairnrunner

import (
	"bytes"
	"testing"

	"github.com/chainreactors/utils/pty"
)

func TestPTYMessageRoundTrip(t *testing.T) {
	want := pty.Frame{
		Type:      pty.FrameInput,
		StreamID:  "terminal-1",
		SessionID: "session-1",
		Data:      []byte{0xff, 0x00, 'x'},
	}
	msg := newPTYMessage(want)
	if msg.T != "pty" {
		t.Fatalf("envelope type = %q", msg.T)
	}
	got, err := decodePTYMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || got.SessionID != want.SessionID || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip frame = %+v", got)
	}
}

func TestDecodePTYMessageRejectsMissingFrame(t *testing.T) {
	if _, err := decodePTYMessage(message{T: "pty"}); err == nil {
		t.Fatal("expected missing frame error")
	}
}
