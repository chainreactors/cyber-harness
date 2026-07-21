package webproto

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/chainreactors/utils/pty"
)

func TestDecodeChatPayloadCarriesExecutorContext(t *testing.T) {
	raw := json.RawMessage(`{
		"session_id":" session-1 ",
		"system_prompt":" be concise ",
		"output_schema":{"name":"Result","schema":{"type":"object"}}
	}`)
	payload, err := DecodeChatPayload(raw)
	if err != nil {
		t.Fatalf("DecodeChatPayload() error = %v", err)
	}
	if payload.SessionID != "session-1" || payload.SystemPrompt != "be concise" {
		t.Fatalf("decoded chat payload = %+v", payload)
	}
	if payload.OutputSchema == nil || payload.OutputSchema.Name != "Result" {
		t.Fatalf("output_schema = %#v", payload.OutputSchema)
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
