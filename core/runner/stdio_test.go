package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestJoinSystemPrompts(t *testing.T) {
	if got := joinSystemPrompts("aiscan", "caller"); got != "aiscan\n\ncaller" {
		t.Fatalf("joinSystemPrompts() = %q", got)
	}
}

func TestResponseFormatFromWebProto(t *testing.T) {
	format, instruction, err := responseFormatFromWebProto(&webproto.OutputSchema{
		Name:   "Result",
		Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}, provider.StructuredOutputJSONSchema)
	if err != nil {
		t.Fatalf("responseFormatFromWebProto() error = %v", err)
	}
	if format.JSONSchema == nil || !format.JSONSchema.Strict || format.JSONSchema.Name != "Result" {
		t.Fatalf("response format = %#v", format)
	}
	if instruction != "" {
		t.Fatalf("instruction = %q, want empty", instruction)
	}
}

func TestResponseFormatFromWebProtoDeepSeek(t *testing.T) {
	format, instruction, err := responseFormatFromWebProto(&webproto.OutputSchema{
		Name:   "Result",
		Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}, provider.StructuredOutputJSONObject)
	if err != nil {
		t.Fatalf("responseFormatFromWebProto() error = %v", err)
	}
	if format.Type != "json_object" || format.JSONSchema != nil {
		t.Fatalf("response format = %#v", format)
	}
	if !strings.Contains(instruction, `"answer"`) {
		t.Fatalf("instruction = %q", instruction)
	}
}

func TestRunStdioRejectsNonChatMessage(t *testing.T) {
	input := bytes.NewBufferString(`{"type":"exec","task_id":"task-1","data":"whoami"}`)
	err := RunStdio(context.Background(), nil, telemetry.NopLogger(), input, &bytes.Buffer{})
	if err == nil || err.Error() != `unsupported webproto message type "exec"` {
		t.Fatalf("RunStdio() error = %v", err)
	}
}

func TestStdioSenderCancelsOnWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sender := newStdioSender(failingWriter{}, cancel)
	err := sender.Send(webproto.Message{Type: "complete", TaskID: "task-1"})
	if err == nil {
		t.Fatal("Send() error = nil")
	}
	if !errors.Is(context.Cause(ctx), err) {
		t.Fatalf("context cause = %v, want %v", context.Cause(ctx), err)
	}
}

func TestStdioSenderEncodesTypedAOPFrame(t *testing.T) {
	_, cancel := context.WithCancelCause(context.Background())
	var output bytes.Buffer
	sender := newStdioSender(&output, cancel)
	event := aop.Event{
		Type:      aop.TypeText,
		TS:        "2026-07-19T00:00:00Z",
		SessionID: "session-1",
		Agent:     "aiscan",
		Data:      webproto.MustJSON(aop.TextData{Content: "hello"}),
	}

	if err := sender.SendAOP("task-1", event); err != nil {
		t.Fatalf("SendAOP() error = %v", err)
	}

	var frame webproto.Message
	if err := json.NewDecoder(&output).Decode(&frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if frame.Type != "aop.text" || frame.TaskID != "task-1" {
		t.Fatalf("frame = %#v", frame)
	}
	var got aop.Event
	if err := json.Unmarshal(frame.Payload, &got); err != nil {
		t.Fatalf("decode AOP payload: %v", err)
	}
	if got.Type != event.Type || !got.Valid() {
		t.Fatalf("AOP payload = %#v", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
