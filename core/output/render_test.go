package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseLineReadsNativeAOPEnvelope(t *testing.T) {
	event := renderEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("hello")},
	}}})
	raw, err := protojson.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	parsed := new(aop.Event)
	if err := protojson.Unmarshal(raw, parsed); err != nil {
		t.Fatal(err)
	}
	if markdown := BuildEventMarkdown([]*aop.Event{parsed}); !strings.Contains(markdown, "hello") {
		t.Fatalf("event markdown = %q", markdown)
	}
}

func TestEventRendererRendersStructuredToolResult(t *testing.T) {
	event := renderEvent(&aop.Event{Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{
		CallId: "call-1", Name: "scan", Output: []*aop.Content{
			aop.Text("three ports"), aop.Image("image/png", []byte("x")),
		},
	}}})
	markdown := BuildEventMarkdown([]*aop.Event{event})
	if !strings.Contains(markdown, "three ports") {
		t.Fatalf("event markdown = %q", markdown)
	}
}

func TestEventRendererFormatsPreformattedCommandAtPresentationBoundary(t *testing.T) {
	event := renderEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "command-1", Role: "assistant", Content: []*aop.Content{aop.Text("one\ntwo")},
	}}})
	_ = types.SetCommandDetail(event, &types.CommandDetail{Line: "/status", Presentation: "preformatted"})
	markdown := BuildEventMarkdown([]*aop.Event{event})
	if !strings.Contains(markdown, "```\none\ntwo\n```") {
		t.Fatalf("event markdown = %q", markdown)
	}
}

func TestEventRendererDoesNotRenderStructuredArtifactPayloads(t *testing.T) {
	extension, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Kind: toolpb.ArtifactKindService, Target: "127.0.0.1:443", Data: []byte(`{"secret":"structured-only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	event := renderEvent(&aop.Event{Payload: &aop.Event_Extension{Extension: extension}})
	markdown := BuildEventMarkdown([]*aop.Event{event})
	if strings.Contains(markdown, "structured-only") || strings.Contains(markdown, "127.0.0.1:443") {
		t.Fatalf("artifact leaked into generic markdown: %q", markdown)
	}
}

func TestRenderEventFileFormatsTheSameAOPJSONLStream(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "session.jsonl")
	outputPath := filepath.Join(dir, "session.md")
	bus := eventbus.New[*aop.Event]()
	writer, err := NewJSONLRecorder(bus, inputPath)
	if err != nil {
		t.Fatal(err)
	}
	events := []*aop.Event{
		renderEvent(&aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}}),
		renderEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-1", Role: "user", Content: []*aop.Content{aop.Text("rendered prompt")}}}}),
		renderEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{Id: "m-2", Role: "assistant", Content: []*aop.Content{aop.Text("rendered answer")}}}}),
	}
	for _, event := range events {
		bus.Emit(event)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RenderEventFile(inputPath, "markdown", outputPath); err != nil {
		t.Fatalf("RenderEventFile: %v", err)
	}
	rendered, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	for _, expected := range []string{"test-model", "rendered prompt", "rendered answer"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatted output missing %q:\n%s", expected, text)
		}
	}
}

func TestEventRendererSeparatesContinuationSessionHeaders(t *testing.T) {
	root := renderEvent(&aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: "test-model"}}})
	root.SessionId = "root-1"
	continuation := renderEvent(&aop.Event{Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{
		Model: "test-model", ParentSessionId: "root-1",
	}}})
	continuation.SessionId = "cont-2"

	markdown := BuildEventMarkdown([]*aop.Event{root, continuation})
	if strings.Count(markdown, "# Agent ") != 2 {
		t.Fatalf("session headers = %q", markdown)
	}
	if !strings.Contains(markdown, "# Agent `root-1`") || !strings.Contains(markdown, "# Agent `cont-2 ← root-1`") {
		t.Fatalf("continuation headers = %q", markdown)
	}
	if strings.Contains(markdown, "# Agent `root-1 ← root-1`") {
		t.Fatalf("root header inherited continuation parent: %q", markdown)
	}
}

func renderEvent(event *aop.Event) *aop.Event {
	event.Id = "e-1"
	event.SessionId = "session-1"
	event.TurnId = "turn-1"
	event.Emitter = "aiscan"
	event.EmittedAt = timestamppb.New(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))
	return event
}
