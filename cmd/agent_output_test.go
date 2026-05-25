package cmd

import (
	"bytes"
	"context"
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
	output := &agentOutput{
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
	output := &agentOutput{
		stdout: &bytes.Buffer{},
		stderr: &stderr,
		tools:  make(map[string]agentToolSummary),
	}

	err := output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Arguments:  `{"command":"scan -i 127.0.0.1 --mode quick"}`,
	})
	if err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	err = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     "ok",
	})
	if err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "bash") || !strings.Contains(got, "scan -i 127.0.0.1 --mode quick") {
		t.Fatalf("stderr missing tool summary: %q", got)
	}
	if !strings.Contains(got, "⎿") {
		t.Fatalf("stderr missing ⎿ marker: %q", got)
	}
}

func TestAgentOutputToolDebugDetails(t *testing.T) {
	var stderr bytes.Buffer
	output := &agentOutput{
		stdout: &bytes.Buffer{},
		stderr: &stderr,
		debug:  true,
		tools:  make(map[string]agentToolSummary),
	}

	_ = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "read",
		Arguments:  `{"path":"docs/usage.md","limit":20}`,
	})
	_ = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "read",
		Result:     "file content",
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "read") || !strings.Contains(got, "docs/usage.md") {
		t.Fatalf("stderr missing read summary: %q", got)
	}
	if !strings.Contains(got, `args: {"path":"docs/usage.md","limit":20}`) {
		t.Fatalf("stderr missing compact args in debug mode: %q", got)
	}
	if !strings.Contains(got, "file content") {
		t.Fatalf("stderr missing result content in debug mode: %q", got)
	}
}

func TestAgentOutputToolError(t *testing.T) {
	var stderr bytes.Buffer
	output := &agentOutput{
		stdout: &bytes.Buffer{},
		stderr: &stderr,
		tools:  make(map[string]agentToolSummary),
	}

	_ = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     "permission denied",
		IsError:    true,
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "permission denied") {
		t.Fatalf("stderr missing tool error: %q", got)
	}
}

func TestAgentOutputWriteEditSummary(t *testing.T) {
	var stderr bytes.Buffer
	output := &agentOutput{
		stdout: &bytes.Buffer{},
		stderr: &stderr,
		tools:  make(map[string]agentToolSummary),
	}

	_ = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "write",
		Arguments:  `{"path":"src/main.go","edits":[{"old_text":"foo","new_text":"bar"},{"old_text":"baz","new_text":"qux"}]}`,
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "src/main.go") {
		t.Fatalf("stderr missing file path: %q", got)
	}
	if !strings.Contains(got, "2 change(s)") {
		t.Fatalf("stderr missing edit count: %q", got)
	}
}

func TestAgentOutputMultiLineResult(t *testing.T) {
	var stderr bytes.Buffer
	output := &agentOutput{
		stdout: &bytes.Buffer{},
		stderr: &stderr,
		tools:  make(map[string]agentToolSummary),
	}

	result := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8"
	_ = output.HandleEvent(context.Background(), agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     result,
	})

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "line1") {
		t.Fatalf("stderr missing first line: %q", got)
	}
	if !strings.Contains(got, "+") && !strings.Contains(got, "lines") {
		t.Fatalf("stderr missing truncation hint for multi-line result: %q", got)
	}
}
