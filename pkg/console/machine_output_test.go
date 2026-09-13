package console

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestMachineOutputJSONProducesOneResultDocument(t *testing.T) {
	var output bytes.Buffer
	renderer := newMachineOutput(&output, "json")
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
	if result.Usage == nil || result.Usage.TotalTokens != 13 || result.Usage.Model != "test-model" {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestMachineOutputJSONReportsExecutionFailure(t *testing.T) {
	var output bytes.Buffer
	renderer := newMachineOutput(&output, "json")
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
	renderer := newMachineOutput(&output, "stream-json")
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
	renderer := newMachineOutput(shortMachineWriter{}, "stream-json")
	renderer.HandleEvent(&aop.Event{Id: "event-1", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
	if err := renderer.Close(); !strings.Contains(err.Error(), io.ErrShortWrite.Error()) {
		t.Fatalf("Close error = %v", err)
	}
}
