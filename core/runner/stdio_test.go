package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
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

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
