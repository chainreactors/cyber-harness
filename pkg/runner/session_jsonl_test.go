package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLoadResumeStateRebuildsCanonicalTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	artifact, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Kind: toolpb.ArtifactKindService, Data: []byte(`{"ip":"127.0.0.1"}`), CallId: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	writeSessionEvents(t, path, []*aop.Event{
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-7", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-8", Role: "assistant", Content: []*aop.Content{aop.Text("working")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{CallId: "call-1", Name: "gogo", Output: []*aop.Content{aop.Text("done")}}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Extension{Extension: artifact}}),
		sessionTestEvent("child", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{ParentSessionId: "root", ParentToolCallId: "call-child"}}}),
		sessionTestEvent("child", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-99", Role: "assistant", Content: []*aop.Content{aop.Text("child")}}}}),
	})

	data, err := loadResumeState(path)
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

func TestListSavedSessionsOnlyReadsJSONL(t *testing.T) {
	dir := t.TempDir()
	writeSessionEvents(t, filepath.Join(dir, "session.jsonl"), []*aop.Event{
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{}}}),
		sessionTestEvent("root", &aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("hello")}}}}),
	})
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(`{"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := listSavedSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || filepath.Base(sessions[0].Path) != "session.jsonl" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func sessionTestEvent(sessionID string, event *aop.Event) *aop.Event {
	event.Id = "event"
	event.SessionId = sessionID
	event.TurnId = "turn-1"
	event.Emitter = "aiscan"
	event.EmittedAt = timestamppb.New(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))
	return event
}

func writeSessionEvents(t *testing.T, path string, events []*aop.Event) {
	t.Helper()
	bus := eventbus.New[*aop.Event]()
	recorder, err := output.NewJSONLRecorder(bus, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		bus.Emit(event)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}
