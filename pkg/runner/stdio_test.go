package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
)

func newTestStdioHost(output io.Writer) *stdioHost {
	return newStdioHost(context.Background(), nil, telemetry.NopLogger(), output)
}

func protocolLine(t *testing.T, id string, message protobuf.Message) string {
	t.Helper()
	data, err := protojson.Marshal(aop.MustWrap(id, "", message))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func openSessionLine(t *testing.T, sessionID string) string {
	id := "open-" + sessionID
	return protocolLine(t, id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{
		SessionId: sessionID,
	}}})
}

func runLine(t *testing.T, sessionID, turnID, text string) string {
	return protocolLine(t, turnID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: &aop.RunTurnRequest{
		SessionId: sessionID, TurnId: turnID,
		Input: &aop.Message{Id: "input-" + turnID, Role: "user", Content: []*aop.Content{aop.Text(text)}},
	}}})
}

func closeSessionLine(t *testing.T, sessionID, reason string) string {
	id := "close-" + sessionID
	return protocolLine(t, id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{
		SessionId: sessionID, Reason: reason,
	}}})
}

func TestStdioAcceptRejectsMalformedJSON(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept("not json")
	envelopes := decodeEnvelopes(t, &output)
	message := unwrapCore(t, envelopes[0])
	if len(envelopes) != 1 || message.GetProtocolError() == nil || !strings.Contains(message.GetProtocolError().Message, "decode frame") {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioAcceptRejectsUnsupportedFrame(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept(protocolLine(t, "future", &aop.ProtocolMessage{}))
	envelopes := decodeEnvelopes(t, &output)
	if len(envelopes) != 1 || unwrapCore(t, envelopes[0]).GetProtocolError() == nil || envelopes[0].ReplyTo != "future" {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioRunRequiresOpenSession(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(runLine(t, "s1", "turn-1", "hello"))
	envelopes := decodeEnvelopes(t, &output)
	if len(envelopes) != 1 || unwrapCore(t, envelopes[0]).GetRunTurnResponse().GetRejected() == nil {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioRunRejectsEmptyPrompt(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(runLine(t, "s1", "turn-1", "   "))
	h.drain()
	envelopes := decodeEnvelopes(t, &output)
	var rejected bool
	for _, envelope := range envelopes {
		message, err := aop.Unwrap(envelope)
		if err == nil {
			if core, ok := message.(*aop.ProtocolMessage); ok && core.GetRunTurnResponse().GetRejected() != nil {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioCommandUsesIndependentCorrelationID(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(protocolLine(t, "command-correlation", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Request{Request: &types.CommandRequest{
		SessionId: "s1", Line: "/help",
	}}}))
	h.drain()
	for _, envelope := range decodeEnvelopes(t, &output) {
		message, err := aop.Unwrap(envelope)
		command, ok := message.(*types.CommandProtocolMessage)
		if err != nil || !ok || command.GetResult() == nil {
			continue
		}
		if envelope.ReplyTo != "command-correlation" {
			t.Fatalf("command result correlation = %+v", envelope)
		}
		return
	}
	t.Fatal("command result missing")
}

func TestStdioHostReportsEncoderFailure(t *testing.T) {
	h := newTestStdioHost(failingWriter{})
	h.emitError("", errors.New("broken"))
	if err := h.err(); err == nil || !strings.Contains(err.Error(), "write stdio protocol") {
		t.Fatalf("host err = %v", err)
	}
}

func TestStdioDrainWithoutRuns(t *testing.T) {
	var output bytes.Buffer
	newTestStdioHost(&output).drain()
}

func decodeEnvelopes(t *testing.T, input *bytes.Buffer) []*aop.Envelope {
	t.Helper()
	var envelopes []*aop.Envelope
	scanner := bufio.NewScanner(bytes.NewReader(input.Bytes()))
	for scanner.Scan() {
		envelope := new(aop.Envelope)
		if err := protojson.Unmarshal(scanner.Bytes(), envelope); err != nil {
			t.Fatal(err)
		}
		envelopes = append(envelopes, envelope)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return envelopes
}

func unwrapCore(t *testing.T, envelope *aop.Envelope) *aop.ProtocolMessage {
	t.Helper()
	message, err := aop.Unwrap(envelope)
	if err != nil {
		t.Fatal(err)
	}
	core, ok := message.(*aop.ProtocolMessage)
	if !ok {
		t.Fatalf("message = %T", message)
	}
	return core
}

func decodeAOPMessages(envelopes []*aop.Envelope) []*aop.Event {
	var events []*aop.Event
	for _, envelope := range envelopes {
		message, err := aop.Unwrap(envelope)
		if err != nil {
			continue
		}
		if core, ok := message.(*aop.ProtocolMessage); ok && core.GetEvent() != nil {
			events = append(events, core.GetEvent())
		}
	}
	return events
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
