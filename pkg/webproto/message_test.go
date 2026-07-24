package webproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/utils/pty"
)

func TestRunFrameRoundTrip(t *testing.T) {
	want := RunPayload{SessionID: "session-1", Parts: []aop.MessagePart{{Type: aop.PartText, Text: "audit target"}}, NoEcho: true, EvalCriteria: "find one SQLi", EvalMaxRounds: 5}
	msg := Message{Type: TypeRun, TurnID: "turn-1", Payload: MustJSON(want)}
	var got RunPayload
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if msg.TurnID != "turn-1" || got.SessionID != want.SessionID || len(got.Parts) != 1 || got.Parts[0].Text != "audit target" || !got.NoEcho || got.EvalMaxRounds != 5 {
		t.Fatalf("run frame = %+v %+v", msg, got)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"turn_id":"turn-1"`) || strings.Contains(string(encoded), "run_id") {
		t.Fatalf("run frame JSON = %s", encoded)
	}
}

func TestPTYMessageRoundTrip(t *testing.T) {
	want := pty.Frame{
		Type:      pty.FrameOutput,
		StreamID:  "terminal-1",
		SessionID: "session-1",
		Data:      []byte{0xff, 0x00, 'x'},
		Sessions: []pty.Info{{
			ID:          "session-1",
			State:       pty.StateRunning,
			ActivitySeq: 2,
			OutputBytes: 10,
		}},
	}

	msg := NewPTYMessage(want)
	if msg.Type != TypePTY || msg.Data != "" || msg.DataB64 != "" {
		t.Fatalf("PTY envelope = %+v", msg)
	}
	got, err := DecodePTYMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || got.SessionID != want.SessionID || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip frame = %+v", got)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ActivitySeq != 2 || got.Sessions[0].OutputBytes != 10 {
		t.Fatalf("round trip sessions = %+v", got.Sessions)
	}
}

func TestDecodePTYMessageRejectsInvalidEnvelope(t *testing.T) {
	if _, err := DecodePTYMessage(Message{Type: "pty.open"}); err == nil {
		t.Fatal("expected invalid envelope error")
	}
	if _, err := DecodePTYMessage(Message{Type: TypePTY, Payload: json.RawMessage(`{"stream_id":"x"}`)}); err == nil {
		t.Fatal("expected missing frame type error")
	}
}
