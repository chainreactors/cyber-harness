package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestJoinSystemPrompts(t *testing.T) {
	if got := joinSystemPrompts("aiscan", "caller"); got != "aiscan\n\ncaller" {
		t.Fatalf("joinSystemPrompts() = %q", got)
	}
}

func TestResponseFormatFromSchema(t *testing.T) {
	format, instruction, err := responseFormatFromSchema(&StdioOutputSchema{
		Name:   "Result",
		Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}, provider.StructuredOutputJSONSchema)
	if err != nil {
		t.Fatalf("responseFormatFromSchema() error = %v", err)
	}
	if format.JSONSchema == nil || !format.JSONSchema.Strict || format.JSONSchema.Name != "Result" {
		t.Fatalf("response format = %#v", format)
	}
	if instruction != "" {
		t.Fatalf("instruction = %q, want empty", instruction)
	}
}

func TestResponseFormatFromSchemaDeepSeek(t *testing.T) {
	format, instruction, err := responseFormatFromSchema(&StdioOutputSchema{
		Name:   "Result",
		Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}, provider.StructuredOutputJSONObject)
	if err != nil {
		t.Fatalf("responseFormatFromSchema() error = %v", err)
	}
	if format.Type != "json_object" || format.JSONSchema != nil {
		t.Fatalf("response format = %#v", format)
	}
	if !strings.Contains(instruction, `"answer"`) {
		t.Fatalf("instruction = %q", instruction)
	}
}

func TestRunStdioRejectsEmptyPromptWithAOPTerminal(t *testing.T) {
	input := bytes.NewBufferString(`{"type":"exec","task_id":"task-1","data":"whoami"}`)
	var output bytes.Buffer
	err := RunStdio(context.Background(), nil, telemetry.NopLogger(), input, &output)
	if err == nil || err.Error() != "stdio prompt is empty" {
		t.Fatalf("RunStdio() error = %v", err)
	}
	events := decodeAOPLines(t, &output)
	if len(events) != 2 || events[0].Type != aop.TypeError || events[1].Type != aop.TypeSessionEnd {
		t.Fatalf("events = %#v", events)
	}
}

func TestStdioSenderCancelsOnWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sender := newStdioSender(failingWriter{}, cancel, "session-1")
	err := sender.Complete()
	if err == nil {
		t.Fatal("Send() error = nil")
	}
	if !errors.Is(context.Cause(ctx), err) {
		t.Fatalf("context cause = %v, want %v", context.Cause(ctx), err)
	}
}

func TestStdioSenderEncodesRawAOP(t *testing.T) {
	_, cancel := context.WithCancelCause(context.Background())
	var output bytes.Buffer
	sender := newStdioSender(&output, cancel, "session-1")
	event := aop.Event{
		Type:      aop.TypeText,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "session-1",
		Agent:     "aiscan",
		Data:      mustJSON(t, aop.TextData{Content: "hello"}),
	}

	if err := sender.Send(event); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var got aop.Event
	if err := json.NewDecoder(&output).Decode(&got); err != nil {
		t.Fatalf("decode AOP: %v", err)
	}
	if got.Type != event.Type || !got.Valid() {
		t.Fatalf("AOP event = %#v", got)
	}
}

func TestStdioSenderDoesNotDuplicateRootTerminal(t *testing.T) {
	_, cancel := context.WithCancelCause(context.Background())
	var output bytes.Buffer
	sender := newStdioSender(&output, cancel, "fallback")
	start := aop.Event{
		Type: aop.TypeSessionStart, TS: "2026-07-19T00:00:00Z", SessionID: "root", Agent: "aiscan",
		Data: mustJSON(t, aop.SessionStartData{}),
	}
	end := aop.Event{
		Type: aop.TypeSessionEnd, TS: "2026-07-19T00:00:01Z", SessionID: "root", Agent: "aiscan",
		Data: mustJSON(t, aop.SessionEndData{Stop: "completed"}),
	}
	if err := sender.Send(start); err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(end); err != nil {
		t.Fatal(err)
	}
	if err := sender.Complete(); err != nil {
		t.Fatal(err)
	}
	events := decodeAOPLines(t, &output)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
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
