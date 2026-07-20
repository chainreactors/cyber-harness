package output

import (
	"strings"
	"testing"
)

func TestParseLineReadsNativeAOPEnvelope(t *testing.T) {
	raw := []byte(`{"type":"text","ts":"2026-07-20T00:00:00Z","session_id":"session-1","agent":"aiscan","data":{"content":"hello","role":"assistant"}}`)

	entry, ok := parseLine(raw)
	if !ok {
		t.Fatal("native AOP envelope was not parsed")
	}
	if _, ok := entry.Data.(*AOPTimelineEntry); !ok {
		t.Fatalf("entry data type = %T", entry.Data)
	}
	if markdown := BuildTimelineMarkdown([]TimelineEntry{entry}); !strings.Contains(markdown, "hello") {
		t.Fatalf("timeline markdown = %q", markdown)
	}
}

func TestParseLineRejectsLegacyAOPRecordPrefix(t *testing.T) {
	record := NewRecord(RecordType("aop.text"), map[string]any{"content": "legacy"})
	if _, ok := parseLine(record.Marshal()); ok {
		t.Fatal("legacy aop.* record should not be accepted after native AOP cutover")
	}
}
