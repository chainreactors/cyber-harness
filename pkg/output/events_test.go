package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestMachineOutputAggregatesRepairTurnsWithoutDoubleCounting(t *testing.T) {
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "json")
	for _, id := range []string{"initial", "repair"} {
		renderer.HandleEvent(&aop.Event{TurnId: id, Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
		usage := &aop.TokenUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13, Model: "fixture", Detail: map[string]uint64{"cached_tokens": 4}}
		// TurnEnded repeats totals already represented by provider usage events.
		renderer.HandleEvent(&aop.Event{TurnId: id, Payload: &aop.Event_Usage{Usage: usage}})
		renderer.HandleEvent(&aop.Event{TurnId: id, Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "completed", Usage: usage}}})
	}
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	var result machineResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 26 || result.Usage.InputTokens != 20 || result.Usage.Detail["cached_tokens"] != 8 {
		t.Fatalf("usage=%+v", result.Usage)
	}
}

func TestMachineOutputDoesNotCarryPreviousAnswerIntoFailedRepair(t *testing.T) {
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "json")
	renderer.HandleEvent(&aop.Event{TurnId: "initial", Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text("obsolete success")}}}})
	renderer.HandleEvent(&aop.Event{TurnId: "repair", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}})
	renderer.SetError(io.ErrUnexpectedEOF)
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	var result machineResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Result != "" {
		t.Fatalf("stale result: %+v", result)
	}
}

func TestMachineOutputJSONProducesOneResultDocument(t *testing.T) {
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "json")
	renderer.HandleEvent(&aop.Event{
		SessionId: "session-1", TurnId: "turn-1",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: "message-1", Role: "assistant", Content: []*aop.Content{aop.Text("final answer")},
		}},
	})
	renderer.HandleEvent(&aop.Event{
		SessionId: "session-1", TurnId: "turn-1",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
			StopReason: "completed",
			Usage:      &aop.TokenUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13, Model: "test-model"},
		}},
	})
	if err := renderer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(output.String()), "\n"); got != 0 {
		t.Fatalf("json output contains %d embedded newlines: %q", got, output.String())
	}
	var result machineResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Type != "result" || result.SessionID != "session-1" || result.TurnID != "turn-1" {
		t.Fatalf("identity = %#v", result)
	}
	if result.IsError || result.StopReason != "completed" || result.Result != "final answer" {
		t.Fatalf("result = %#v", result)
	}
	// The public result uses snake_case keys and JSON numbers, including after
	// replacing the redundant projection with the canonical usage type.
	if !strings.Contains(output.String(), `"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"model":"test-model"}`) {
		t.Fatalf("usage wire contract changed: %s", output.String())
	}
	if result.Usage == nil || result.Usage.TotalTokens != 13 || result.Usage.Model != "test-model" {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestMachineOutputJSONReportsExecutionFailure(t *testing.T) {
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "json")
	renderer.SetError(io.ErrUnexpectedEOF)
	if err := renderer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var result machineResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.IsError || result.Error == nil || !strings.Contains(result.Error.Message, io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("failure result = %#v", result)
	}
}

func TestMachineOutputStreamJSONWritesTypedAOPJSONL(t *testing.T) {
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "stream-json")
	events := []*aop.Event{
		{Id: "event-1", SessionId: "session-1", Seq: 1, Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}},
		{Id: "event-2", SessionId: "session-1", Seq: 2, Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}},
	}
	for _, event := range events {
		renderer.HandleEvent(event)
	}
	if err := renderer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(events) {
		t.Fatalf("lines = %d, want %d: %q", len(lines), len(events), output.String())
	}
	for i, line := range lines {
		decoded := new(aop.Event)
		if err := protojson.Unmarshal([]byte(line), decoded); err != nil {
			t.Fatalf("decode line %d: %v", i, err)
		}
		if decoded.Id != events[i].Id || decoded.Seq != events[i].Seq {
			t.Fatalf("line %d = %#v", i, decoded)
		}
	}
}

type shortMachineWriter struct{}

func (shortMachineWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestMachineOutputReportsShortWrite(t *testing.T) {
	renderer := NewEventWriter(shortMachineWriter{}, "stream-json")
	renderer.HandleEvent(&aop.Event{Id: "event-1", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	if err := renderer.Close(); !strings.Contains(err.Error(), io.ErrShortWrite.Error()) {
		t.Fatalf("Close error = %v", err)
	}
}

func TestJSONPreservesNativeScannerArtifacts(t *testing.T) {
	artifact := &toolpb.Artifact{ResultId: "result", Tool: "gogo", Kind: "service", Target: "127.0.0.1:80", Data: []byte(`{"port":80,"serial":9007199254740993}`)}
	extension, err := anypb.New(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	renderer := NewEventWriter(&output, "json")
	renderer.HandleEvent(&aop.Event{SessionId: "scan", Payload: &aop.Event_Extension{Extension: extension}})
	// An observer must retain its own artifact snapshot.
	extension.Value = nil
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	var result machineResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 1 || !proto.Equal(result.Artifacts[0], artifact) {
		t.Fatalf("lost native scanner result: %s", output.String())
	}
}
