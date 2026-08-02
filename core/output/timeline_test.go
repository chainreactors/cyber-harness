package output

import (
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseLineReadsNativeAOPEnvelope(t *testing.T) {
	event := timelineEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "m-1", Role: "assistant", Content: []*aop.Content{aop.Text("hello")},
	}}})
	raw, err := protojson.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	entry, ok := parseLine(raw)
	if !ok {
		t.Fatal("native AOP event was not parsed")
	}
	if _, ok := entry.Data.(*aop.Event); !ok {
		t.Fatalf("entry data type = %T", entry.Data)
	}
	if markdown := BuildTimelineMarkdown([]TimelineEntry{entry}); !strings.Contains(markdown, "hello") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func TestTimelineRendersStructuredToolResult(t *testing.T) {
	event := timelineEvent(&aop.Event{Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{
		CallId: "call-1", Name: "scan", Output: []*aop.Content{
			aop.Text("three ports"), aop.Image("image/png", []byte("x")),
		},
	}}})
	markdown := BuildTimelineMarkdown([]TimelineEntry{{Timestamp: event.EmittedAt.AsTime(), Type: aop.Kind(event), Data: event}})
	if !strings.Contains(markdown, "three ports") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func TestTimelineFormatsPreformattedCommandAtPresentationBoundary(t *testing.T) {
	event := timelineEvent(&aop.Event{Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "command-1", Role: "assistant", Content: []*aop.Content{aop.Text("one\ntwo")},
	}}})
	_ = types.SetCommandDetail(event, &types.CommandDetail{Line: "/status", Presentation: "preformatted"})
	markdown := BuildTimelineMarkdown([]TimelineEntry{{Timestamp: event.EmittedAt.AsTime(), Type: aop.Kind(event), Data: event}})
	if !strings.Contains(markdown, "```\none\ntwo\n```") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func timelineEvent(event *aop.Event) *aop.Event {
	event.Id = "e-1"
	event.SessionId = "session-1"
	event.TurnId = "turn-1"
	event.Emitter = "aiscan"
	event.EmittedAt = timestamppb.New(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))
	return event
}
