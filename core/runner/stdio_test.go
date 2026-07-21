package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func newTestStdioHost(output io.Writer) *stdioHost {
	return newStdioHost(context.Background(), nil, telemetry.NopLogger(), output)
}

func userMessageLine(t *testing.T, sessionID, text string) string {
	t.Helper()
	event := aop.Event{
		Type:      aop.TypeMessage,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: sessionID,
		Agent:     "test",
		Data: mustJSON(t, aop.MessageData{
			MessageID: "m-1",
			Role:      "user",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: text}},
		}),
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStdioAcceptRejectsMalformedJSON(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)

	h.accept("not json")

	events := decodeAOPLines(t, &output)
	if len(events) != 1 || events[0].Type != aop.TypeError {
		t.Fatalf("events = %#v", events)
	}
	data, err := aop.DecodeData[aop.ErrorData](events[0])
	if err != nil || !strings.Contains(data.Message, "decode inbound event") {
		t.Fatalf("error data = %+v, %v", data, err)
	}
}

func TestStdioAcceptIgnoresNonMessageEvents(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)

	event := aop.Event{
		Type:      aop.TypeTurnStart,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "s1",
		Agent:     "test",
		Data:      mustJSON(t, aop.TurnData{Turn: 1}),
	}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	h.accept(string(line))

	if output.Len() != 0 {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestStdioAcceptRejectsNonUserMessage(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)

	event := aop.Event{
		Type:      aop.TypeMessage,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "s1",
		Agent:     "test",
		Data: mustJSON(t, aop.MessageData{
			MessageID: "m-1",
			Role:      "assistant",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: "hi"}},
		}),
	}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	h.accept(string(line))

	events := decodeAOPLines(t, &output)
	if len(events) != 1 || events[0].Type != aop.TypeError {
		t.Fatalf("events = %#v", events)
	}
}

func TestStdioSessionQueueFullEmitsError(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	sess := &stdioSession{
		id:    "s1",
		queue: make(chan stdioQueuedMessage, stdioQueueCapacity),
		done:  make(chan struct{}),
	}
	h.sessions["s1"] = sess

	line := userMessageLine(t, "s1", "hello")
	for i := 0; i < stdioQueueCapacity; i++ {
		h.accept(line)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected output before overflow: %q", output.String())
	}

	h.accept(line)
	events := decodeAOPLines(t, &output)
	if len(events) != 1 || events[0].Type != aop.TypeError {
		t.Fatalf("events = %#v", events)
	}
	data, err := aop.DecodeData[aop.ErrorData](events[0])
	if err != nil || !strings.Contains(data.Message, "queue full") {
		t.Fatalf("error data = %+v, %v", data, err)
	}
}

func TestStdioRunOneRejectsEmptyPrompt(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	sess := &stdioSession{id: "s1"}

	h.runOne(sess, stdioQueuedMessage{
		data: aop.MessageData{
			Role:  "user",
			Parts: []aop.MessagePart{{Type: aop.PartText, Text: "   "}},
		},
	})

	events := decodeAOPLines(t, &output)
	if len(events) != 1 || events[0].Type != aop.TypeError {
		t.Fatalf("events = %#v", events)
	}
	data, err := aop.DecodeData[aop.ErrorData](events[0])
	if err != nil || !strings.Contains(data.Message, "empty prompt") {
		t.Fatalf("error data = %+v, %v", data, err)
	}
}

func TestStdioHostReportsEncoderFailure(t *testing.T) {
	h := newTestStdioHost(failingWriter{})
	h.failAll(errors.New("broken"))

	if err := h.err(); err == nil || !strings.Contains(err.Error(), "write AOP stdout") {
		t.Fatalf("host err = %v", err)
	}
}

func TestStdioDrainWithoutSessions(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.drain() // must not block
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeAOPLines(t *testing.T, input *bytes.Buffer) []aop.Event {
	t.Helper()
	var events []aop.Event
	decoder := json.NewDecoder(input)
	for {
		var event aop.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
