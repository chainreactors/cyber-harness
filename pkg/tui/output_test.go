package tui

import (
	"bytes"

	"regexp"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
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
	output := &AgentOutput{
		stdout:   &stdout,
		stderr:   &bytes.Buffer{},
		markdown: false,
	}

	output.Final("## Report\n\nDone.")

	got := stdout.String()
	if !strings.Contains(got, "## Report") || !strings.Contains(got, "Done.") {
		t.Fatalf("final output missing markdown content: %q", got)
	}
	if strings.Contains(got, "[assistant]") || strings.Contains(got, "[final_report]") {
		t.Fatalf("final output contains legacy wrapper: %q", got)
	}
}

func TestAgentOutputToolSummary(t *testing.T) {
	var stderr bytes.Buffer
	output := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

	output.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Arguments:  `{"command":"scan -i 127.0.0.1 --mode quick"}`,
	})
	output.HandleEvent(agent.Event{
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
	output := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		debug:     true,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

	output.HandleEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "read",
		Arguments:  `{"path":"docs/usage.md","limit":20}`,
	})
	output.HandleEvent(agent.Event{
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
	output := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

	output.HandleEvent(agent.Event{
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
	output := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

	output.HandleEvent(agent.Event{
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
	output := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

	result := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20"
	output.HandleEvent(agent.Event{
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
	if strings.Contains(got, "│") {
		t.Fatalf("stderr should not contain │ border character: %q", got)
	}
}

func TestFormatToolArguments(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		wantKeys  []string
	}{
		{
			name:      "bash command",
			toolName:  "bash",
			arguments: `{"command":"ls -la"}`,
			wantKeys:  []string{"command"},
		},
		{
			name:      "read with offset",
			toolName:  "read",
			arguments: `{"path":"main.go","offset":10,"limit":50}`,
			wantKeys:  []string{"path", "offset", "limit"},
		},
		{
			name:      "read skips zero offset",
			toolName:  "read",
			arguments: `{"path":"main.go","offset":0}`,
			wantKeys:  []string{"path"},
		},
		{
			name:      "write with edits",
			toolName:  "write",
			arguments: `{"path":"a.go","edits":[{"old_text":"x","new_text":"y"}]}`,
			wantKeys:  []string{"path", "edits"},
		},
		{
			name:      "glob",
			toolName:  "glob",
			arguments: `{"pattern":"*.go","path":"src/"}`,
			wantKeys:  []string{"pattern", "path"},
		},
		{
			name:      "unknown tool uses all keys sorted",
			toolName:  "custom",
			arguments: `{"z_key":"z","a_key":"a"}`,
			wantKeys:  []string{"a_key", "z_key"},
		},
		{
			name:      "empty args",
			toolName:  "bash",
			arguments: `{}`,
			wantKeys:  nil,
		},
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
		input    string
		wantTool string
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
	o := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 0,
		tools:     make(map[string]agentToolSummary),
	}

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
	o := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

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
	o := &AgentOutput{
		stdout:    &bytes.Buffer{},
		stderr:    &stderr,
		verbosity: 1,
		tools:     make(map[string]agentToolSummary),
	}

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
