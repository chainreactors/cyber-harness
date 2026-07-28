package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

func newTestStdioHost(output io.Writer) *stdioHost {
	return newStdioHost(context.Background(), nil, telemetry.NopLogger(), output)
}

func protocolLine(t *testing.T, message webproto.Message) string {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func openSessionLine(t *testing.T, sessionID string) string {
	return protocolLine(t, webproto.Message{Type: webproto.TypeSessionOpen, Payload: mustJSON(t, webproto.SessionOpenPayload{SessionID: sessionID})})
}

func runLine(t *testing.T, sessionID, turnID, text string) string {
	return protocolLine(t, webproto.Message{
		Type: webproto.TypeRun, TurnID: turnID,
		Payload: mustJSON(t, webproto.RunPayload{SessionID: sessionID, Parts: []aop.MessagePart{{Type: aop.PartText, Text: text}}}),
	})
}

func TestStdioAcceptRejectsMalformedJSON(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept("not json")
	messages := decodeProtocolLines(t, &output)
	if len(messages) != 1 || messages[0].Type != webproto.TypeError {
		t.Fatalf("messages = %#v", messages)
	}
	var data webproto.ErrorPayload
	_ = json.Unmarshal(messages[0].Payload, &data)
	if !strings.Contains(data.Message, "decode frame") {
		t.Fatalf("error data = %+v", data)
	}
}

func TestStdioAcceptRejectsUnsupportedFrame(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept(protocolLine(t, webproto.Message{Type: "future.frame"}))
	messages := decodeProtocolLines(t, &output)
	if len(messages) != 1 || messages[0].Type != webproto.TypeError {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestStdioRunRequiresOpenSession(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(runLine(t, "s1", "turn-1", "hello"))
	messages := decodeProtocolLines(t, &output)
	if len(messages) != 1 || messages[0].Type != webproto.TypeError {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestStdioRunRejectsEmptyPrompt(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(runLine(t, "s1", "turn-1", "   "))
	h.drain()
	messages := decodeProtocolLines(t, &output)
	if messages[len(messages)-1].Type != webproto.TypeError {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestStdioCommandUsesTaskIDCorrelation(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(protocolLine(t, webproto.Message{
		Type:   webproto.TypeCommand,
		TaskID: "command-1",
		Payload: mustJSON(t, webproto.CommandPayload{
			SessionID: "s1",
			Line:      "/help",
		}),
	}))
	h.drain()
	messages := decodeProtocolLines(t, &output)
	for _, message := range messages {
		if message.Type != webproto.TypeCommandResult {
			continue
		}
		if message.TaskID != "command-1" || message.TurnID != "" {
			t.Fatalf("command result correlation = %+v", message)
		}
		return
	}
	t.Fatalf("messages = %#v", messages)
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
	h := newTestStdioHost(&output)
	h.drain()
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeProtocolLines(t *testing.T, input *bytes.Buffer) []webproto.Message {
	t.Helper()
	var messages []webproto.Message
	decoder := json.NewDecoder(input)
	for {
		var message webproto.Message
		if err := decoder.Decode(&message); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	return messages
}

func decodeAOPMessages(t *testing.T, messages []webproto.Message) []aop.Event {
	t.Helper()
	var events []aop.Event
	for _, message := range messages {
		if message.Type != webproto.TypeAOP {
			continue
		}
		var event aop.Event
		if err := json.Unmarshal(message.Payload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
