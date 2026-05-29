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
	toolResultPreviewLines  = 3
	toolResultPreviewWidth  = 160
)

// ANSI escape helpers
var (
	colorReset = "\033[0m"
	colorDim   = "\033[2m"
	colorBold  = "\033[1m"
	colorRed   = "\033[31m"
	colorCyan  = "\033[36m"
)

type agentOutput struct {
	stdout   io.Writer
	stderr   io.Writer
	markdown bool
	color    bool
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
	noColor := false
	if option != nil {
		debug = option.Debug
		quiet = option.Quiet
		noColor = option.NoColor
	}
	useColor := !noColor && term.IsTerminal(int(os.Stderr.Fd()))
	if !useColor {
		colorReset = ""
		colorDim = ""
		colorBold = ""
		colorRed = ""
		colorCyan = ""
	}
	return &agentOutput{
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		markdown: markdown,
		color:    useColor,
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
		fmt.Fprintf(o.stderr, "%s> %s%s\n", colorBold, label, colorReset)
		return
	}
	fmt.Fprintf(o.stderr, "%s> %s:%s %s\n", colorBold, label, colorReset, text)
}

func (o *agentOutput) Empty() {
	if o == nil || o.quiet {
		return
	}
	fmt.Fprintf(o.stderr, "%sNo output.%s\n", colorDim, colorReset)
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
	case agent.EventTurnEnd:
		o.turnEnd(event)
	}
	return nil
}

func (o *agentOutput) turnEnd(event agent.Event) {
	if o.quiet || event.Usage == nil {
		return
	}
	cache := ""
	if event.Usage.CacheReadTokens > 0 || event.Usage.CacheWriteTokens > 0 {
		cache = fmt.Sprintf(" cache_read=%d cache_write=%d (%.0f%%)",
			event.Usage.CacheReadTokens, event.Usage.CacheWriteTokens,
			event.Usage.CacheHitRatio()*100)
	}
	fmt.Fprintf(o.stderr, "%s[turn %d] prompt=%d completion=%d total=%d context=%d%s%s\n",
		colorDim, event.Turn,
		event.Usage.PromptTokens, event.Usage.CompletionTokens, event.Usage.TotalTokens,
		event.ContextTokens, cache,
		colorReset)
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

	// Render tool header: ⎿ tool_name: summary
	header := fmt.Sprintf("%s⎿ %s%s%s", colorDim, colorCyan, name, colorReset)
	if summary != "" {
		header += fmt.Sprintf("%s: %s%s", colorDim, colorReset, summary)
	}
	fmt.Fprintln(o.stderr, header)

	if o.debug {
		if args := compactAgentJSON(event.Arguments, agentDebugPreviewLimit); args != "" {
			fmt.Fprintf(o.stderr, "  %sargs: %s%s\n", colorDim, args, colorReset)
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
		fmt.Fprintf(o.stderr, "  %s⎿ %s%s%s\n", colorDim, colorRed, compactAgentLine(errText, agentStatusPreviewLimit), colorReset)
		return
	}

	result := strings.TrimSpace(event.Result)
	if result == "" {
		return
	}

	o.renderToolResult(event.ToolName, result)
}

func (o *agentOutput) renderToolResult(toolName, result string) {
	lines := strings.Split(result, "\n")

	// Determine how many preview lines to show
	maxLines := toolResultPreviewLines
	if o.debug {
		maxLines = 20
	}

	// Adjust for specific tool types that benefit from more context
	switch toolName {
	case "read":
		maxLines = 5
	case "write":
		maxLines = 6 // edit summary can have multiple lines
	}

	showLines := lines
	truncated := false
	if len(showLines) > maxLines {
		showLines = showLines[:maxLines]
		truncated = true
	}

	for _, line := range showLines {
		display := line
		if len(display) > toolResultPreviewWidth {
			display = display[:toolResultPreviewWidth] + "…"
		}
		fmt.Fprintf(o.stderr, "  %s⎿%s %s\n", colorDim, colorReset, display)
	}

	if truncated {
		remaining := len(lines) - maxLines
		fmt.Fprintf(o.stderr, "  %s⎿ … +%d lines%s\n", colorDim, remaining, colorReset)
	}
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
	case "read":
		path := stringArg(args, "path")
		if offset := stringArg(args, "offset"); offset != "" && offset != "0" {
			path += fmt.Sprintf(" (offset=%s)", offset)
		}
		return compactAgentLine(path, agentStatusPreviewLimit)
	case "write":
		path := stringArg(args, "path")
		if edits, ok := args["edits"]; ok && edits != nil {
			if arr, ok := edits.([]any); ok {
				path += fmt.Sprintf(" (edit: %d change(s))", len(arr))
			}
		}
		return compactAgentLine(path, agentStatusPreviewLimit)
	case "glob":
		return compactAgentLine(joinAgentSummaryParts(
			stringArg(args, "pattern"),
			prefixedArg("in ", stringArg(args, "path")),
		), agentStatusPreviewLimit)
	case "subagent":
		action := stringArg(args, "action")
		if action == "" || action == "create" {
			mode := stringArg(args, "mode")
			typeName := stringArg(args, "type")
			prompt := compactAgentLine(stringArg(args, "prompt"), 80)
			return joinAgentSummaryParts(typeName, prefixedArg("mode=", mode), prompt)
		}
		return joinAgentSummaryParts(action, stringArg(args, "name"))
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
		return value[:limit] + "…"
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
