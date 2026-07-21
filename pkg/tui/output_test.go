package tui

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// ---------------------------------------------------------------------------
// AOP event builders
// ---------------------------------------------------------------------------

func aopTestEvent(typ string, data any, ext map[string]any) aop.Event {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	ev := aop.Event{Type: typ, Data: raw}
	if ext != nil {
		ev.Ext = map[string]any{"aiscan": ext}
	}
	return ev
}

func turnStartEvent(turn int) aop.Event {
	return aopTestEvent(aop.TypeTurnStart, aop.TurnData{Turn: turn}, nil)
}

func turnEndEvent(turn, contextTokens int) aop.Event {
	return aopTestEvent(aop.TypeTurnEnd, aop.TurnData{Turn: turn}, map[string]any{
		"context_tokens": contextTokens,
	})
}

func textDeltaEvent(messageID, delta string) aop.Event {
	return aopTestEvent(aop.TypeMessageDelta, aop.MessageDeltaData{
		MessageID: messageID,
		PartType:  aop.PartText,
		Delta:     delta,
	}, nil)
}

func reasoningDeltaEvent(messageID, delta string) aop.Event {
	return aopTestEvent(aop.TypeMessageDelta, aop.MessageDeltaData{
		MessageID: messageID,
		PartType:  aop.PartReasoning,
		Delta:     delta,
	}, nil)
}

func messageEvent(messageID, role string, parts ...aop.MessagePart) aop.Event {
	return aopTestEvent(aop.TypeMessage, aop.MessageData{
		MessageID: messageID,
		Role:      role,
		Parts:     parts,
	}, nil)
}

func toolCallEvent(id, name, args string) aop.Event {
	return aopTestEvent(aop.TypeToolCall, aop.ToolCallData{
		ToolCallID: id,
		ToolName:   name,
		Args:       args,
	}, nil)
}

func toolResultEvent(id, name, result string, isError bool) aop.Event {
	return aopTestEvent(aop.TypeToolResult, aop.ToolResultData{
		ToolCallID: id,
		ToolName:   name,
		Content:    result,
		IsError:    isError,
	}, nil)
}

func usageEvent(input, outputTok, total int) aop.Event {
	return aopTestEvent(aop.TypeUsage, aop.UsageData{
		InputTokens:  input,
		OutputTokens: outputTok,
		TotalTokens:  total,
	}, nil)
}

func statusEvent(state string, ext map[string]any) aop.Event {
	return aopTestEvent(aop.TypeStatus, aop.StatusData{State: state}, ext)
}

func testOutput(stderr io.Writer, verbosity int, debug bool) *AgentOutput {
	stdout := &bytes.Buffer{}
	color := output.NewColor(false)
	o := &AgentOutput{
		color:     color,
		debug:     debug,
		verbosity: verbosity,
		stream:    NewStreamWriter(stdout, stderr, true, false, color, verbosity),
		deltas:    make(map[string]*deltaAccumulator),
	}
	o.live = NewLiveStatus(NewLiveView(stderr, ""), o.dim, o.renderToolLine)
	return o
}

func liveRunning(l *LiveStatus) bool {
	return l.Running()
}

func TestRenderAgentMarkdownPlainFallback(t *testing.T) {
	got := renderAgentMarkdown("  ## Title\n\n- item  ", false)
	want := "## Title\n\n- item"
	if got != want {
		t.Fatalf("renderAgentMarkdown() = %q, want %q", got, want)
	}
}

func TestAgentOutputFinalWritesPlainMarkdownWithoutWrapper(t *testing.T) {
	var stdout bytes.Buffer
	color := output.NewColor(false)
	o := &AgentOutput{
		color:  color,
		stream: NewStreamWriter(&stdout, &bytes.Buffer{}, true, false, color, 0),
		deltas: make(map[string]*deltaAccumulator),
	}
	o.live = NewLiveStatus(NewLiveView(&bytes.Buffer{}, ""), o.dim, o.renderToolLine)

	o.Final("## Report\n\nDone.")

	got := stdout.String()
	if !strings.Contains(got, "## Report") || !strings.Contains(got, "Done.") {
		t.Fatalf("final output missing markdown content: %q", got)
	}
}

func TestThinkingSpinnerSurvivesInvisibleStreamUpdates(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if !liveRunning(o.live) {
		t.Fatal("thinking spinner did not start")
	}

	o.HandleEvent(textDeltaEvent("m-1", ""))
	if !liveRunning(o.live) {
		t.Fatal("empty stream update stopped thinking spinner")
	}

	o.HandleEvent(reasoningDeltaEvent("m-1", "internal reasoning that is hidden at default verbosity"))
	if !liveRunning(o.live) {
		t.Fatal("hidden reasoning stream update stopped thinking spinner")
	}

	o.HandleEvent(textDeltaEvent("m-1", "partial paragraph without markdown flush"))
	if !liveRunning(o.live) {
		t.Fatal("buffered markdown stream update stopped thinking spinner before visible output")
	}

	o.HandleEvent(textDeltaEvent("m-1", "\n\n"))
	if !liveRunning(o.live) {
		t.Fatal("visible stream update stopped thinking spinner")
	}
	if !strings.Contains(stdout.String(), "partial paragraph") {
		t.Fatalf("visible content was not written: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestNonTTYMessageUpdateBuffersUntilTurnEnd(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, false)

	content := "buffered answer"
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", content))
	if stdout.Len() != 0 {
		t.Fatalf("non-TTY update streamed stdout before turn end: %q", stdout.String())
	}

	o.HandleEvent(messageEvent("m-1", "assistant", aop.MessagePart{Type: aop.PartText, Text: content}))
	o.HandleEvent(turnEndEvent(1, 0))
	if !strings.Contains(stdout.String(), content) {
		t.Fatalf("non-TTY turn end did not render content: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStaticOutputDisablesDynamicTUIOnTTY(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewStaticAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if liveRunning(o.live) {
		t.Fatal("static output started thinking live view")
	}

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo hi"}`))
	if liveRunning(o.live) {
		t.Fatal("static output started tool live view")
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "▸") || !strings.Contains(got, "bash") || !strings.Contains(got, "echo hi") {
		t.Fatalf("static tool output missing direct rendering: %q", got)
	}
	if strings.Contains(stderr.String(), syncBegin) || strings.Contains(stderr.String(), eraseLine) {
		t.Fatalf("static output wrote dynamic ANSI controls: %q", stderr.String())
	}
}

func TestThinkingLineShowsTokenUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(1000, 234, 1234))
	o.HandleEvent(textDeltaEvent("m-1", ""))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "tokens=1,234") {
		t.Fatalf("thinking line missing token usage: %q", got)
	}
	if !liveRunning(o.live) {
		t.Fatal("usage update stopped thinking spinner")
	}
}

func TestInteractiveInputSuppressesLiveStatus(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.SetInteractiveInputActive(true)
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(1000, 234, 1234))
	o.HandleEvent(textDeltaEvent("m-1", "hello"))

	got := stripANSI(stderr.String())
	if strings.Contains(got, "thinking") || strings.Contains(got, "tokens=1,234") {
		t.Fatalf("live status leaked while input active: %q", got)
	}
	if liveRunning(o.live) {
		t.Fatal("live spinner started while input was active")
	}
}

func TestLiveStatusShowsCumulativeContextAndCurrentOutputTokens(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		LLMOptions: cfg.LLMOptions{Model: "gpt-4"},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(400, 100, 1000))
	o.HandleEvent(turnEndEvent(1, 400))

	o.HandleEvent(turnStartEvent(2))
	o.HandleEvent(usageEvent(4096, 50, 2000))
	o.HandleEvent(textDeltaEvent("m-2", ""))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "tokens=3,000") {
		t.Fatalf("live line missing cumulative tokens: %q", got)
	}
	if !strings.Contains(got, "ctx=4,096/8,192 (50%)") {
		t.Fatalf("live line missing context percentage: %q", got)
	}
	if !strings.Contains(got, "out=50") {
		t.Fatalf("live line missing current output tokens: %q", got)
	}
}

func TestTurnStatsShowsContextWindowUse(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		LLMOptions: cfg.LLMOptions{Model: "gpt-4"},
	}, &stdout, &stderr, true)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(4096, 50, 4146))
	o.HandleEvent(turnEndEvent(1, 4096))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "turn 1") ||
		!strings.Contains(got, "input=4,096 output=50") ||
		!strings.Contains(got, "ctx=4,096/8,192 (50%)") {
		t.Fatalf("turn stats missing context window use: %q", got)
	}
}

func TestLiveStatusSwitchesTalkingAndTooling(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if o.live.Status() != liveStatusThinking {
		t.Fatalf("live status = %q, want thinking", o.live.Status())
	}

	o.HandleEvent(textDeltaEvent("m-1", "partial assistant answer"))
	if o.live.Status() != liveStatusTalking {
		t.Fatalf("live status = %q, want talking", o.live.Status())
	}

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo hi"}`))
	if o.live.Status() != liveStatusTooling {
		t.Fatalf("live status = %q, want tooling", o.live.Status())
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, liveStatusTalking) || !strings.Contains(got, liveStatusTooling) {
		t.Fatalf("live output missing status labels: %q", got)
	}
}

func TestThinkingVerboseStreamsReasoningWithoutTags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true, true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	reasoning := "checking target scope\nprobing admin route"
	o.HandleEvent(reasoningDeltaEvent("m-1", reasoning))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "checking target scope") || !strings.Contains(got, "probing admin route") {
		t.Fatalf("streamed thinking block missing reasoning: %q", got)
	}
	if !liveRunning(o.live) {
		t.Fatal("thinking spinner stopped while reasoning was streamed")
	}
	if strings.Contains(stderr.String(), "<thinking>") {
		t.Fatalf("reasoning tag was printed: %q", stderr.String())
	}
	if o.stream.ReasoningPrinted() != len(reasoning) {
		t.Fatalf("reasoning printed = %d, want %d", o.stream.ReasoningPrinted(), len(reasoning))
	}
}

func TestThinkingVerboseStreamsOnlyReasoningDelta(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true, true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", "The user wants"))
	o.HandleEvent(reasoningDeltaEvent("m-1", " me to test redhaze.top"))

	got := stripANSI(stderr.String())
	if strings.Count(got, "The user wants") != 1 {
		t.Fatalf("reasoning prefix rendered repeatedly: %q", got)
	}
	if !strings.Contains(got, "me to test redhaze.top") {
		t.Fatalf("reasoning delta not streamed correctly: %q", got)
	}
}

func TestThinkingBlockFinalRenderingHasNoTags(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 2, false)
	reasoning := "checking target scope\nprobing admin route"

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(messageEvent("m-1", "assistant", aop.MessagePart{Type: aop.PartReasoning, Text: reasoning}))
	o.HandleEvent(turnEndEvent(1, 0))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "checking target scope") || !strings.Contains(got, "probing admin route") {
		t.Fatalf("final thinking block missing reasoning: %q", got)
	}
	if strings.Contains(got, "<thinking>") || strings.Contains(got, "</thinking>") {
		t.Fatalf("final thinking block contains tags: %q", got)
	}
}

func TestAgentOutputToolSummary(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"scan -i 127.0.0.1 --mode quick"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "ok", false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "bash") || !strings.Contains(got, "scan -i 127.0.0.1 --mode quick") {
		t.Fatalf("stderr missing tool summary: %q", got)
	}
	if !strings.Contains(got, "▸") {
		t.Fatalf("stderr missing ▸ start marker: %q", got)
	}
	if !strings.Contains(got, "✓") {
		t.Fatalf("stderr missing ✓ end marker: %q", got)
	}
	if !strings.Contains(got, "command") {
		t.Fatalf("stderr missing structured arg key 'command': %q", got)
	}
}

func TestAgentOutputToolDebugDetails(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, true)

	o.HandleEvent(toolCallEvent("call-1", "read", `{"path":"docs/usage.md","limit":20}`))
	o.HandleEvent(toolResultEvent("call-1", "read", "file content", false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "read") || !strings.Contains(got, "docs/usage.md") {
		t.Fatalf("stderr missing read summary: %q", got)
	}
	if !strings.Contains(got, `raw: {"path":"docs/usage.md","limit":20}`) {
		t.Fatalf("stderr missing compact args in debug mode: %q", got)
	}
	if !strings.Contains(got, "file content") {
		t.Fatalf("stderr missing result content in debug mode: %q", got)
	}
}

func TestAgentOutputToolError(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolResultEvent("call-1", "bash", "permission denied", true))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✗") {
		t.Fatalf("stderr missing ✗ error marker: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Fatalf("stderr missing tool error: %q", got)
	}
}

func TestAgentOutputWriteEditSummary(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolCallEvent("call-1", "write", `{"path":"src/main.go","edits":[{"old_text":"foo","new_text":"bar"},{"old_text":"baz","new_text":"qux"}]}`))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "▸") {
		t.Fatalf("stderr missing ▸ marker: %q", got)
	}
	if !strings.Contains(got, "src/main.go") {
		t.Fatalf("stderr missing file path: %q", got)
	}
	if !strings.Contains(got, "2 change(s)") {
		t.Fatalf("stderr missing edit count: %q", got)
	}
}

func TestAgentOutputMultiLineResult(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	result := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20"
	o.HandleEvent(toolResultEvent("call-1", "bash", result, false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✓") {
		t.Fatalf("stderr missing ✓ marker: %q", got)
	}
	if !strings.Contains(got, "line1") {
		t.Fatalf("stderr missing first line: %q", got)
	}
	if !strings.Contains(got, "+") && !strings.Contains(got, "lines") {
		t.Fatalf("stderr missing truncation hint for multi-line result: %q", got)
	}
}

func TestFormatToolArguments(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		wantKeys  []string
	}{
		{"bash command", "bash", `{"command":"ls -la"}`, []string{"command"}},
		{"read with offset", "read", `{"path":"main.go","offset":10,"limit":50}`, []string{"path", "offset", "limit"}},
		{"read skips zero offset", "read", `{"path":"main.go","offset":0}`, []string{"path"}},
		{"write with edits", "write", `{"path":"a.go","edits":[{"old_text":"x","new_text":"y"}]}`, []string{"path", "edits"}},
		{"glob", "glob", `{"pattern":"*.go","path":"src/"}`, []string{"pattern", "path"}},
		{"unknown tool uses all keys sorted", "custom", `{"z_key":"z","a_key":"a"}`, []string{"a_key", "z_key"}},
		{"empty args", "bash", `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := formatToolArguments(tt.toolName, tt.arguments)
			if tt.wantKeys == nil {
				if len(lines) != 0 {
					t.Fatalf("expected no lines, got %d", len(lines))
				}
				return
			}
			if len(lines) != len(tt.wantKeys) {
				t.Fatalf("expected %d lines, got %d: %+v", len(tt.wantKeys), len(lines), lines)
			}
			for i, wk := range tt.wantKeys {
				if lines[i].key != wk {
					t.Errorf("line[%d].key = %q, want %q", i, lines[i].key, wk)
				}
			}
		})
	}
}

func TestExtractPseudoCommand(t *testing.T) {
	tests := []struct {
		input      string
		wantTool   string
		wantTarget string
	}{
		{"scan -i 10.0.0.1 --mode quick", "scan", "10.0.0.1"},
		{"gogo -i 10.0.0.0/24 --ports top1000", "gogo", "10.0.0.0/24"},
		{"ls -la", "", ""},
		{"neutron http://target.com", "neutron", "http://target.com"},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tool, target := extractPseudoCommand(tt.input)
			if tool != tt.wantTool {
				t.Errorf("tool = %q, want %q", tool, tt.wantTool)
			}
			if target != tt.wantTarget {
				t.Errorf("target = %q, want %q", target, tt.wantTarget)
			}
		})
	}
}

func TestToolCallCounting(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(toolResultEvent("c1", "bash", "ok", false))
	o.HandleEvent(toolResultEvent("c2", "read", "data", false))
	o.HandleEvent(toolResultEvent("c3", "bash", "fail", true))

	if o.toolCallCount != 3 {
		t.Errorf("toolCallCount = %d, want 3", o.toolCallCount)
	}
	if o.toolErrorCount != 1 {
		t.Errorf("toolErrorCount = %d, want 1", o.toolErrorCount)
	}
}

func TestTurnStartMarker(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(turnStartEvent(1))
	turn1Output := stderr.String()

	o.HandleEvent(turnStartEvent(2))
	turn2Output := stderr.String()[len(turn1Output):]

	got1 := stripANSI(turn1Output)
	if strings.Contains(got1, "turn 1") {
		t.Fatalf("turn 1 should not show turn marker, got: %q", got1)
	}

	got2 := stripANSI(turn2Output)
	if !strings.Contains(got2, "turn 2") {
		t.Fatalf("turn 2 should show turn marker, got: %q", got2)
	}
}

func TestEvalEndRendering(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(statusEvent(agent.StatusEvalEnd, map[string]any{
		"eval_round": 0, "eval_pass": true, "eval_reason": "all checks passed",
	}))
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✓") || !strings.Contains(got, "eval") || !strings.Contains(got, "pass") {
		t.Fatalf("eval pass missing expected markers: %q", got)
	}
	if !strings.Contains(got, "all checks passed") {
		t.Fatalf("eval pass missing reason: %q", got)
	}

	stderr.Reset()
	o.HandleEvent(statusEvent(agent.StatusEvalEnd, map[string]any{
		"eval_round": 1, "eval_pass": false, "eval_reason": "port 443 not scanned",
	}))
	got = stripANSI(stderr.String())
	if !strings.Contains(got, "⟳") || !strings.Contains(got, "fail") {
		t.Fatalf("eval fail missing expected markers: %q", got)
	}
}

func TestCompleteMessageClearsDeltaAccumulator(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", "hello"))
	o.HandleEvent(messageEvent("m-1", "assistant", aop.MessagePart{Type: aop.PartText, Text: "hello"}))
	if len(o.deltas) != 0 {
		t.Fatalf("delta accumulator not cleared on complete message: %d entries", len(o.deltas))
	}
	if !o.hasAssistant {
		t.Fatal("complete assistant message not recorded")
	}
}
