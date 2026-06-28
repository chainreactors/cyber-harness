package webproto

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/utils/pty"
)

func TestPTYResponsePayloadRoundTripPreservesSessions(t *testing.T) {
	info := tmux.Info{ID: "session-1", Kind: "repl", Name: "main-repl", State: tmux.StateRunning}
	msg := FrameToMessage(pty.Frame{
		Type:     pty.FrameSessions,
		StreamID: "term-1",
		Sessions: []tmux.Info{info},
	})

	frame, err := MessageToFrame(msg)
	if err != nil {
		t.Fatalf("MessageToFrame() error = %v", err)
	}
	if len(frame.Sessions) != 1 || frame.Sessions[0].ID != info.ID || frame.Sessions[0].Kind != info.Kind {
		t.Fatalf("sessions not preserved: %+v", frame.Sessions)
	}

	normalized := FrameToMessage(frame)
	normalizedFrame, err := MessageToFrame(normalized)
	if err != nil {
		t.Fatalf("normalized MessageToFrame() error = %v", err)
	}
	if len(normalizedFrame.Sessions) != 1 || normalizedFrame.Sessions[0].ID != info.ID {
		t.Fatalf("normalized sessions not preserved: %+v", normalizedFrame.Sessions)
	}
}

func TestPTYResponsePayloadRoundTripPreservesAttachedSession(t *testing.T) {
	info := tmux.Info{ID: "session-1", Kind: "repl", Name: "main-repl", State: tmux.StateRunning}
	msg := FrameToMessage(pty.Frame{
		Type:      pty.FrameAttached,
		StreamID:  "term-1",
		SessionID: info.ID,
		Session:   &info,
	})

	frame, err := MessageToFrame(msg)
	if err != nil {
		t.Fatalf("MessageToFrame() error = %v", err)
	}
	if frame.Session == nil || frame.Session.ID != info.ID || frame.SessionID != info.ID {
		t.Fatalf("attached session not preserved: frame=%+v session=%+v", frame, frame.Session)
	}
}

func TestPTYOutputPreservesSessionID(t *testing.T) {
	msg := FrameToMessage(pty.Frame{
		Type:      pty.FrameOutput,
		StreamID:  "term-1",
		SessionID: "session-1",
		Data:      []byte("hello\n"),
	})
	if msg.Data != "hello\n" {
		t.Fatalf("output data = %q", msg.Data)
	}
	var payload PTYPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.SessionID != "session-1" {
		t.Fatalf("session id lost: %+v", payload)
	}

	frame, err := MessageToFrame(msg)
	if err != nil {
		t.Fatalf("MessageToFrame: %v", err)
	}
	if frame.SessionID != "session-1" || string(frame.Data) != "hello\n" {
		t.Fatalf("round-trip lost output fields: %+v data=%q", frame, frame.Data)
	}
}

func TestDecodePTYPayloadError(t *testing.T) {
	if _, err := DecodePTYPayload(json.RawMessage(`{invalid`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	p, err := DecodePTYPayload(nil)
	if err != nil {
		t.Fatalf("nil input: %v", err)
	}
	if p.Kind != "" || p.SessionID != "" {
		t.Fatalf("expected zero value, got %+v", p)
	}
}

func TestMessageFrameRoundTripSingleton(t *testing.T) {
	payload, _ := json.Marshal(PTYPayload{
		Kind: "repl", Name: "main-repl", Singleton: true,
		SessionID: "sess-42", Cols: 120, Rows: 40, Data: "hello",
	})
	msg := Message{Type: "pty.open", StreamID: "s1", Payload: payload}

	frame, err := MessageToFrame(msg)
	if err != nil {
		t.Fatalf("MessageToFrame: %v", err)
	}
	if !frame.Singleton || frame.Kind != "repl" || frame.Name != "main-repl" {
		t.Fatalf("fields lost: %+v", frame)
	}
	if frame.Cols != 120 || frame.Rows != 40 || string(frame.Data) != "hello" {
		t.Fatalf("data lost: cols=%d rows=%d data=%q", frame.Cols, frame.Rows, frame.Data)
	}

	msg2 := FrameToMessage(frame)
	var p PTYPayload
	_ = json.Unmarshal(msg2.Payload, &p)
	if !p.Singleton || p.Kind != "repl" || p.SessionID != "sess-42" {
		t.Fatalf("round-trip lost: %+v", p)
	}
}

func TestMessageToFrameRejectsInvalidType(t *testing.T) {
	if _, err := MessageToFrame(Message{Type: "not.pty"}); err == nil {
		t.Fatal("expected error for invalid type")
	}
}
