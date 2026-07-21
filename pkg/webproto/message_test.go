package webproto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/utils/pty"
)

func TestIsAOPUserMessageDecodesGoalExt(t *testing.T) {
	event := aop.Event{
		Type:      aop.TypeMessage,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: "session-1",
		Agent:     "aiscan.web",
		Data: MustJSON(aop.MessageData{
			MessageID: "msg-1",
			Role:      "user",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: "audit target"}},
		}),
		Ext: map[string]any{"aiscan": map[string]any{
			"eval_criteria": "find one SQLi",
			"eval_max_rounds": 5,
			"no_echo":         true,
		}},
	}
	msg := Message{Type: "aop", Payload: MustJSON(event)}

	decoded, ok := IsAOPUserMessage(msg)
	if !ok {
		t.Fatal("IsAOPUserMessage() = false, want true")
	}
	if got := UserMessageText(decoded); got != "audit target" {
		t.Fatalf("UserMessageText() = %q, want %q", got, "audit target")
	}
	goal := DecodeGoalExt(decoded)
	if goal.EvalCriteria != "find one SQLi" || goal.EvalMaxRounds != 5 || !goal.NoEcho {
		t.Fatalf("DecodeGoalExt() = %+v", goal)
	}

	if _, ok := IsAOPUserMessage(Message{Type: "exec", Data: "x"}); ok {
		t.Fatal("IsAOPUserMessage(exec) = true, want false")
	}
	assistant := event
	assistant.Data = MustJSON(aop.MessageData{MessageID: "m-2", Role: "assistant", Parts: []aop.MessagePart{{Type: aop.PartText, Text: "hi"}}})
	if _, ok := IsAOPUserMessage(Message{Type: "aop", Payload: MustJSON(assistant)}); ok {
		t.Fatal("IsAOPUserMessage(assistant) = true, want false")
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
