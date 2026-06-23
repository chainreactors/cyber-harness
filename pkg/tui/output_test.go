package tui

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func testOutput(stderr io.Writer, verbosity int, debug bool) *AgentOutput {
	stdout := &bytes.Buffer{}
	color := output.NewColor(false)
	o := &AgentOutput{
		color:     color,
		debug:     debug,
		verbosity: verbosity,
		stream:    NewStreamWriter(stdout, stderr, true, false, color, verbosity),
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
	}
	o.live = NewLiveStatus(NewLiveView(&bytes.Buffer{}, ""), o.dim, o.renderToolLine)

	o.Final("## Report\n\nDone.")

	got := stdout.String()
	if !strings.Contains(got, "## Report") || !strings.Contains(got, "Done.") {
		t.Fatalf("final output missing markdown content: %q", got)
	}
}

func TestThinkingSpinnerSurvivesInvisibleStreamUpdates(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	if !liveRunning(o.live) {
		t.Fatal("thinking spinner did not start")
	}

	o.HandleEvent(agent.Event{Type: agent.EventMessageUpdate, Turn: 1, Message: agent.ChatMessage{Role: "assistant"}})
	if !liveRunning(o.live) {
		t.Fatal("role-only stream update stopped thinking spinner")
	}

	reasoning := "internal reasoning that is hidden at default verbosity"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", ReasoningContent: &reasoning},
	})
	if !liveRunning(o.live) {
		t.Fatal("hidden reasoning stream update stopped thinking spinner")
	}

	content := "partial paragraph without markdown flush"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", Content: &content},
	})
	if !liveRunning(o.live) {
		t.Fatal("buffered markdown stream update stopped thinking spinner before visible output")
	}

	content += "\n\n"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", Content: &content},
	})
	if !liveRunning(o.live) {
		t.Fatal("visible stream update stopped thinking spinner")
	}
	if !strings.Contains(stdout.String(), "partial paragraph") {
		t.Fatalf("visible content was not written: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestNonTTYMessageUpdateBuffersUntilTurnEnd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, false)

	content := "buffered answer"
	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", Content: &content},
	})
	if stdout.Len() != 0 {
		t.Fatalf("non-TTY update streamed stdout before turn end: %q", stdout.String())
	}

	o.HandleEvent(agent.Event{
		Type:    agent.EventTurnEnd,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", Content: &content},
	})
	if !strings.Contains(stdout.String(), content) {
		t.Fatalf("non-TTY turn end did not render content: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStaticOutputDisablesDynamicTUIOnTTY(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewStaticAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	if liveRunning(o.live) {
		t.Fatal("static output started thinking live view")
	}

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Arguments:  `{"command":"echo hi"}`,
	})
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
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	o.HandleEvent(agent.Event{
		Type:  agent.EventMessageUpdate,
		Turn:  1,
		Usage: &agent.Usage{PromptTokens: 1000, CompletionTokens: 234, TotalTokens: 1234},
		Message: agent.ChatMessage{
			Role: "assistant",
		},
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "tokens=1,234") {
		t.Fatalf("thinking line missing token usage: %q", got)
	}
	if !liveRunning(o.live) {
		t.Fatal("usage update stopped thinking spinner")
	}
}

func TestLiveStatusSwitchesTalkingAndTooling(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	if o.live.Status() != liveStatusThinking {
		t.Fatalf("live status = %q, want thinking", o.live.Status())
	}

	content := "partial assistant answer"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", Content: &content},
	})
	if o.live.Status() != liveStatusTalking {
		t.Fatalf("live status = %q, want talking", o.live.Status())
	}

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		Turn:       1,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Arguments:  `{"command":"echo hi"}`,
	})
	if o.live.Status() != liveStatusTooling {
		t.Fatalf("live status = %q, want tooling", o.live.Status())
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, liveStatusTalking) || !strings.Contains(got, liveStatusTooling) {
		t.Fatalf("live output missing status labels: %q", got)
	}
}

func TestThinkingVerboseStreamsReasoningWithoutTags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true, true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	reasoning := "checking target scope\nprobing admin route"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", ReasoningContent: &reasoning},
	})

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
	var stdout, stderr bytes.Buffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true, true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	reasoning := "The user wants"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", ReasoningContent: &reasoning},
	})
	reasoning = "The user wants me to test redhaze.top"
	o.HandleEvent(agent.Event{
		Type:    agent.EventMessageUpdate,
		Turn:    1,
		Message: agent.ChatMessage{Role: "assistant", ReasoningContent: &reasoning},
	})

	got := stripANSI(stderr.String())
	if strings.Count(got, "The user wants") != 1 {
		t.Fatalf("reasoning prefix rendered repeatedly: %q", got)
	}
	if !strings.Contains(got, "me to test redhaze.top") {
		t.Fatalf("reasoning delta not streamed correctly: %q", got)
	}
}

func TestThinkingBlockFinalRenderingHasNoTags(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 2, false)
	reasoning := "checking target scope\nprobing admin route"

	o.HandleEvent(agent.Event{
		Type: agent.EventTurnEnd,
		Turn: 1,
		Message: agent.ChatMessage{
			Role:             "assistant",
			ReasoningContent: &reasoning,
		},
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "checking target scope") || !strings.Contains(got, "probing admin route") {
		t.Fatalf("final thinking block missing reasoning: %q", got)
	}
	if strings.Contains(got, "<thinking>") || strings.Contains(got, "</thinking>") {
		t.Fatalf("final thinking block contains tags: %q", got)
	}
}

func TestAgentOutputToolSummary(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Arguments:  `{"command":"scan -i 127.0.0.1 --mode quick"}`,
	})
	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     "ok",
	})

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
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, true)

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "read",
		Arguments:  `{"path":"docs/usage.md","limit":20}`,
	})
	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "read",
		Result:     "file content",
	})

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
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     "permission denied",
		IsError:    true,
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✗") {
		t.Fatalf("stderr missing ✗ error marker: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Fatalf("stderr missing tool error: %q", got)
	}
}

func TestAgentOutputWriteEditSummary(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "write",
		Arguments:  `{"path":"src/main.go","edits":[{"old_text":"foo","new_text":"bar"},{"old_text":"baz","new_text":"qux"}]}`,
	})

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
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	result := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20"
	o.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     result,
	})

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
	var stderr bytes.Buffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(agent.Event{Type: agent.EventToolExecutionEnd, ToolCallID: "c1", ToolName: "bash", Result: "ok"})
	o.HandleEvent(agent.Event{Type: agent.EventToolExecutionEnd, ToolCallID: "c2", ToolName: "read", Result: "data"})
	o.HandleEvent(agent.Event{Type: agent.EventToolExecutionEnd, ToolCallID: "c3", ToolName: "bash", IsError: true, Result: "fail"})

	if o.toolCallCount != 3 {
		t.Errorf("toolCallCount = %d, want 3", o.toolCallCount)
	}
	if o.toolErrorCount != 1 {
		t.Errorf("toolErrorCount = %d, want 1", o.toolErrorCount)
	}
}

func TestTurnStartMarker(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 1})
	turn1Output := stderr.String()

	o.HandleEvent(agent.Event{Type: agent.EventTurnStart, Turn: 2})
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
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(agent.Event{Type: agent.EventEvalEnd, EvalPass: true, EvalRound: 0, EvalReason: "all checks passed"})
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✓") || !strings.Contains(got, "eval") || !strings.Contains(got, "pass") {
		t.Fatalf("eval pass missing expected markers: %q", got)
	}
	if !strings.Contains(got, "all checks passed") {
		t.Fatalf("eval pass missing reason: %q", got)
	}

	stderr.Reset()
	o.HandleEvent(agent.Event{Type: agent.EventEvalEnd, EvalPass: false, EvalRound: 1, EvalReason: "port 443 not scanned"})
	got = stripANSI(stderr.String())
	if !strings.Contains(got, "⟳") || !strings.Contains(got, "fail") {
		t.Fatalf("eval fail missing expected markers: %q", got)
	}
}
