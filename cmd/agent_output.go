package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

const (
	agentStatusPreviewLimit = 180
	agentDebugPreviewLimit  = 320
)

type agentOutput struct {
	stdout   io.Writer
	stderr   io.Writer
	markdown bool
	debug    bool
	quiet    bool
	tools    map[string]agentToolSummary
}

type agentToolSummary struct {
	name    string
	summary string
}

func newAgentOutput(option *Option) *agentOutput {
	markdown := stdoutMarkdownEnabled(option)
	debug := false
	quiet := false
	if option != nil {
		debug = option.Debug
		quiet = option.Quiet
	}
	return &agentOutput{
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		markdown: markdown,
		debug:    debug,
		quiet:    quiet,
		tools:    make(map[string]agentToolSummary),
	}
}

func stdoutMarkdownEnabled(option *Option) bool {
	if option != nil && option.NoColor {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func (o *agentOutput) Start(label, text string) {
	if o == nil || o.quiet {
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "task"
	}
	text = compactAgentLine(text, agentStatusPreviewLimit)
	if text == "" {
		fmt.Fprintf(o.stderr, "> %s\n", label)
		return
	}
	fmt.Fprintf(o.stderr, "> %s: %s\n", label, text)
}

func (o *agentOutput) Empty() {
	if o == nil || o.quiet {
		return
	}
	fmt.Fprintln(o.stderr, "No output.")
}

func (o *agentOutput) Final(content string) {
	if o == nil {
		return
	}
	rendered := renderAgentMarkdown(content, o.markdown)
	if rendered == "" {
		return
	}
	fmt.Fprintln(o.stdout, rendered)
}

func (o *agentOutput) HandleEvent(_ context.Context, event agent.Event) error {
	if o == nil {
		return nil
	}
	switch event.Type {
	case agent.EventToolExecutionStart:
		o.toolStart(event)
	case agent.EventToolExecutionEnd:
		o.toolEnd(event)
	}
	return nil
}

func (o *agentOutput) toolStart(event agent.Event) {
	name := strings.TrimSpace(event.ToolName)
	if name == "" {
		name = "tool"
	}
	summary := summarizeToolArguments(name, event.Arguments)
	if o.tools == nil {
		o.tools = make(map[string]agentToolSummary)
	}
	if event.ToolCallID != "" {
		o.tools[event.ToolCallID] = agentToolSummary{name: name, summary: summary}
	}
	if o.quiet {
		return
	}
	if summary != "" {
		fmt.Fprintf(o.stderr, "- %s: %s\n", name, summary)
	} else {
		fmt.Fprintf(o.stderr, "- %s\n", name)
	}
	if o.debug {
		if args := compactAgentJSON(event.Arguments, agentDebugPreviewLimit); args != "" {
			fmt.Fprintf(o.stderr, "  args: %s\n", args)
		}
	}
}

func (o *agentOutput) toolEnd(event agent.Event) {
	if o.quiet {
		return
	}
	if event.IsError || event.Err != nil {
		errText := toolErrorText(event)
		if errText == "" {
			errText = "tool execution failed"
		}
		fmt.Fprintf(o.stderr, "  error: %s\n", compactAgentLine(errText, agentStatusPreviewLimit))
		return
	}
	if !o.debug {
		return
	}
	result := compactAgentLine(event.Result, agentDebugPreviewLimit)
	if result == "" {
		result = "ok"
	}
	fmt.Fprintf(o.stderr, "  result: %s\n", result)
}

func toolErrorText(event agent.Event) string {
	if event.Err != nil {
		return event.Err.Error()
	}
	return strings.TrimSpace(event.Result)
}

func renderAgentMarkdown(content string, enabled bool) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if !enabled {
		return content
	}
	r, err := getAgentMarkdownRenderer()
	if err != nil {
		return content
	}
	rendered, err := r.Render(content)
	if err != nil {
		return content
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return content
	}
	return rendered
}

var (
	agentMarkdownRenderer     *glamour.TermRenderer
	agentMarkdownRendererErr  error
	agentMarkdownRendererOnce sync.Once
)

func getAgentMarkdownRenderer() (*glamour.TermRenderer, error) {
	agentMarkdownRendererOnce.Do(func() {
		agentMarkdownRenderer, agentMarkdownRendererErr = glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithColorProfile(termenv.ANSI),
			glamour.WithEmoji(),
		)
	})
	return agentMarkdownRenderer, agentMarkdownRendererErr
}

func summarizeToolArguments(name, arguments string) string {
	args := decodeToolArguments(arguments)
	if len(args) == 0 {
		return ""
	}
	switch name {
	case "bash":
		return compactAgentLine(stringArg(args, "command"), agentStatusPreviewLimit)
	case "read", "write":
		return compactAgentLine(stringArg(args, "path"), agentStatusPreviewLimit)
	case "glob":
		return compactAgentLine(joinAgentSummaryParts(
			stringArg(args, "pattern"),
			prefixedArg("in ", stringArg(args, "path")),
		), agentStatusPreviewLimit)
	case "ioa_space":
		return compactAgentLine(stringArg(args, "name"), agentStatusPreviewLimit)
	case "ioa_send":
		return compactAgentLine(prefixedArg("space ", stringArg(args, "space_id")), agentStatusPreviewLimit)
	case "ioa_read":
		return compactAgentLine(joinAgentSummaryParts(
			prefixedArg("space ", stringArg(args, "space_id")),
			prefixedArg("message ", stringArg(args, "message_id")),
			prefixedArg("after ", stringArg(args, "after")),
		), agentStatusPreviewLimit)
	default:
		return compactAgentLine(firstNonEmptyArg(args, "target", "url", "input", "path", "name"), agentStatusPreviewLimit)
	}
}

func decodeToolArguments(arguments string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil
	}
	return args
}

func stringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64, bool:
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func firstNonEmptyArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringArg(args, key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func prefixedArg(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return prefix + value
}

func joinAgentSummaryParts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

func compactAgentLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func compactAgentJSON(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(value)); err == nil {
		value = buf.String()
	}
	return compactAgentLine(value, limit)
}
