package output

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/aop"
)

func TestParseLineReadsNativeAOPEnvelope(t *testing.T) {
	raw := []byte(`{"type":"message","ts":"2026-07-20T00:00:00Z","session_id":"session-1","agent":"aiscan","data":{"message_id":"m-1","role":"assistant","parts":[{"type":"text","text":"hello"}]}}`)

	entry, ok := parseLine(raw)
	if !ok {
		t.Fatal("native AOP envelope was not parsed")
	}
	if _, ok := entry.Data.(*aop.Event); !ok {
		t.Fatalf("entry data type = %T", entry.Data)
	}
	if markdown := BuildTimelineMarkdown([]TimelineEntry{entry}); !strings.Contains(markdown, "hello") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func TestTimelineRendersStructuredToolResult(t *testing.T) {
	data, _ := json.Marshal(aop.ToolResultData{
		ToolCallID: "call-1", ToolName: "scan",
		Content: aop.ToolResultContent{Content: "three ports", Images: []aop.ImageSource{{MediaType: "image/png", Base64: "eA=="}}},
	})
	event := aop.Event{
		Type: aop.TypeToolResult, TS: "2026-07-20T00:00:00Z", SessionID: "session-1", TurnID: "turn-1", Agent: "aiscan", Data: data,
	}
	markdown := BuildTimelineMarkdown([]TimelineEntry{{Timestamp: mustTimelineTime(t, event.TS), Type: event.Type, Data: &event}})
	if !strings.Contains(markdown, "three ports") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func mustTimelineTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestParseLineRejectsLegacyAgentRecord(t *testing.T) {
	record := NewRecord(RecordType("agent"), map[string]any{"type": "message_end"})
	if _, ok := parseLine(record.Marshal()); ok {
		t.Fatal("legacy agent record should not be accepted")
	}
}

func TestParseLineRejectsLegacyAOPRecordPrefix(t *testing.T) {
	record := NewRecord(RecordType("aop.text"), map[string]any{"content": "legacy"})
	if _, ok := parseLine(record.Marshal()); ok {
		t.Fatal("legacy aop.* record should not be accepted after native AOP cutover")
	}
}
