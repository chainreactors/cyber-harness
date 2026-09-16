package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	coreevents "github.com/chainreactors/cyber/core/events"
	telemetry "github.com/chainreactors/cyber/pkg/exts/telemetry"
	types "github.com/chainreactors/cyber/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLoadResumeStateRebuildsCanonicalTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	artifact, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Kind: toolpb.ArtifactKindService, Data: []byte(`{"ip":"127.0.0.1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := anypb.New(&operationpb.Ref{CallId: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	writeSessionEvents(t, path, []*aop.Event{
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-7", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-8", Role: "assistant", Content: []*aop.Content{aop.Text("working")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{CallId: "call-1", Name: "gogo", Output: []*aop.Content{aop.Text("done")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Extension{Extension: artifact}, Extensions: []*anypb.Any{ref}}),
		sessionTestEvent("child", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{ParentSessionId: "root", ParentToolCallId: "call-child"}}}),
		sessionTestEvent("child", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-99", Role: "assistant", Content: []*aop.Content{aop.Text("child")}}}}),
	})

	data, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if data.SessionID != "root" || data.Model != "test-model" || data.MessageCounter != 8 || len(data.Messages) != 3 {
		t.Fatalf("resume state = %#v", data)
	}
	if result := data.Messages[2].Content[0].GetToolResult(); result == nil || result.CallId != "call-1" {
		t.Fatalf("tool result message = %#v", data.Messages[2])
	}
}

func TestLoadResumeStateRejectsEventsWithoutHistoryMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unversioned.jsonl")
	writeSessionEvents(t, path, []*aop.Event{
		{Id: "start", SessionId: "root", Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{}}},
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}}),
	})
	if _, err := ReadHistory(path); err == nil {
		t.Fatal("loadResumeState accepted an event stream without explicit history metadata")
	}
}

func TestLoadResumeStateRejectsDuplicateEventIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.jsonl")
	start := sessionTestEvent("root", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{}}})
	start.Id = "same-event"
	message := sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}})
	message.Id = "same-event"
	marshal := protojson.MarshalOptions{UseProtoNames: true}
	first, err := marshal.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	second, err := marshal.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	data := append(append(first, '\n'), append(second, '\n')...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHistory(path); err == nil {
		t.Fatal("loadResumeState accepted duplicate event IDs")
	}
}

func sessionTestEvent(sessionID string, event *aop.Event) *aop.Event {
	switch payload := event.Payload.(type) {
	case *aop.Event_SessionStarted:
		event.Id = "event-session-started-" + sessionID
		_ = types.SetSessionHistory(event, &types.SessionHistory{Mode: types.SessionHistory_MODE_INHERIT})
	case *aop.Event_Message:
		event.Id = "event-message-" + payload.Message.Id
	case *aop.Event_ToolResult:
		event.Id = "event-tool-result-" + payload.ToolResult.CallId
	case *aop.Event_Extension:
		event.Id = "event-extension-" + sessionID
	default:
		event.Id = "event-" + sessionID
	}
	event.SessionId = sessionID
	event.TurnId = "turn-1"
	event.Emitter = "cyber"
	event.EmittedAt = timestamppb.New(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))
	return event
}

func writeSessionEvents(t *testing.T, path string, events []*aop.Event) {
	t.Helper()
	bus := coreevents.New()
	output, err := telemetry.New(bus, telemetry.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadtelemetry(t, output); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		bus.Publish(event)
	}
	if err := output.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
