package terminal

import (
	"testing"
	"time"

	"github.com/chainreactors/utils/pty"
)

func TestProtoJSONRoundTrip(t *testing.T) {
	started := time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC)
	want := pty.Frame{
		Type: pty.FrameSessions, StreamID: "stream-1", SessionID: "session-1", Data: []byte("hello"),
		Sessions: []pty.Info{{ID: "session-1", Kind: "repl", StartedAt: started, ActivitySeq: 7}},
	}
	raw, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || got.SessionID != want.SessionID || string(got.Data) != "hello" {
		t.Fatalf("frame = %+v, want %+v", got, want)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "session-1" || !got.Sessions[0].StartedAt.Equal(started) {
		t.Fatalf("sessions = %+v", got.Sessions)
	}
}
