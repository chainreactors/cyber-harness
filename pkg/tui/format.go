package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/util"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// ---------------------------------------------------------------------------
// Tool result preview
// ---------------------------------------------------------------------------

type toolResultPreview struct {
	lines     []string
	truncated bool
	hidden    int
}

func buildToolResultPreview(toolName, result string, debug bool) toolResultPreview {
	lines := normalizeToolResultLines(result)
	if len(lines) == 0 {
		return toolResultPreview{}
	}

	if toolName == "fetch" {
		return buildFetchToolResultPreview(lines, debug)
	}

	maxLines := toolResultPreviewDefault
	if debug {
		maxLines = 20
	}

	switch toolName {
	case "read":
		maxLines = 10
	case "write":
		maxLines = 6
	case "bash":
		maxLines = 12
	}

	return selectToolResultLines(lines, maxLines)
}

func buildFetchToolResultPreview(lines []string, debug bool) toolResultPreview {
	sep := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return selectToolResultLines(lines, toolResultPreviewDefault)
	}

	bodyLines := toolFetchBodyLines
	if debug {
		bodyLines = 16
	}

	selected := make([]string, 0, sep+1+bodyLines)
	selected = append(selected, lines[:sep+1]...)

	bodyKept := 0
	lastSelected := sep
	for i := sep + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		selected = append(selected, line)
		lastSelected = i
		bodyKept++
		if bodyKept >= bodyLines {
			break
		}
	}

	hidden := len(lines) - lastSelected - 1
	if hidden < 0 {
		hidden = 0
	}
	display := make([]string, 0, len(selected))
	for _, line := range selected {
		display = append(display, truncateToolResultLine(line, toolResultPreviewWidth))
	}
	return toolResultPreview{lines: display, truncated: hidden > 0, hidden: hidden}
}

func selectToolResultLines(lines []string, maxLines int) toolResultPreview {
	hidden := 0
	selected := lines
	if maxLines > 0 && maxLines < len(lines) {
		selected = lines[:maxLines]
		hidden = len(lines) - maxLines
	}
	display := make([]string, 0, len(selected))
	for _, line := range selected {
		display = append(display, truncateToolResultLine(line, toolResultPreviewWidth))
	}
	return toolResultPreview{lines: display, truncated: hidden > 0, hidden: hidden}
}

func normalizeToolResultLines(result string) []string {
	result = strings.ReplaceAll(result, "\r\n", "\n")
	result = strings.ReplaceAll(result, "\r", "\n")

	rawLines := strings.Split(result, "\n")
	lines := make([]string, 0, len(rawLines))
	lastBlank := false
	for _, line := range rawLines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(lines) == 0 || lastBlank {
				continue
			}
			lines = append(lines, "")
			lastBlank = true
			continue
		}
		lines = append(lines, line)
		lastBlank = false
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func truncateToolResultLine(value string, limit int) string {
	return truncate.ClipRunes(value, limit)
}

// ---------------------------------------------------------------------------
// Event helpers
// ---------------------------------------------------------------------------

func toolNameOrDefault(ev agent.Event) string {
	if name := strings.TrimSpace(ev.ToolName); name != "" {
		return name
	}
	return "tool"
}

func isToolMetaLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[exit code:") ||
		strings.HasPrefix(trimmed, "[command timed out") ||
		strings.HasPrefix(trimmed, "[truncated:")
}

// ---------------------------------------------------------------------------
// Spinner label helpers
// ---------------------------------------------------------------------------

var knownScanners = map[string]bool{
	"scan": true, "gogo": true, "spray": true, "zombie": true,
	"neutron": true, "katana": true, "passive": true,
}

func extractPseudoCommand(cmdLine string) (tool, target string) {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return "", ""
	}
	cmd := fields[0]
	if !knownScanners[cmd] {
		return "", ""
	}
	for i := 1; i < len(fields); i++ {
		if (fields[i] == "-i" || fields[i] == "--input") && i+1 < len(fields) {
			return cmd, fields[i+1]
		}
	}
	if len(fields) > 1 {
		return cmd, truncate.Clip(strings.Join(fields[1:], " "), 40)
	}
	return cmd, ""
}

// ---------------------------------------------------------------------------
// User intent rendering
// ---------------------------------------------------------------------------

func shouldRenderUserIntent(body string) bool {
	return strings.Contains(strings.TrimRight(body, "\n"), "\n")
}

// ---------------------------------------------------------------------------
// Formatting helpers for stats
// ---------------------------------------------------------------------------

// formatTokenUsage formats token usage like: "input=2,378 output=27 cache 95%"
func formatTokenUsage(u *agent.Usage) string {
	if u == nil {
		return ""
	}
	s := fmt.Sprintf("input=%s output=%s", util.FormatNumber(u.PromptTokens), util.FormatNumber(u.CompletionTokens))
	if ratio := u.CacheHitRatio(); ratio > 0 {
		s += fmt.Sprintf(" cache %.0f%%", ratio*100)
	}
	return s
}

// ---------------------------------------------------------------------------
// Chat message summarisation helpers
// ---------------------------------------------------------------------------

func lastMessageSummary(messages []agent.ChatMessage) (role string, contentLen int, toolCalls int, reasoningLen int, preview string) {
	if len(messages) == 0 {
		return "", 0, 0, 0, ""
	}
	return summarizeChatMessage(messages[len(messages)-1])
}

func summarizeChatMessage(msg agent.ChatMessage) (role string, contentLen int, toolCalls int, reasoningLen int, preview string) {
	role = msg.Role
	if msg.Content != nil {
		contentLen = len(*msg.Content)
		preview = truncate.Clip(*msg.Content, agentDebugPreviewLimit)
	}
	if msg.ReasoningContent != nil {
		reasoningLen = len(*msg.ReasoningContent)
	}
	toolCalls = len(msg.ToolCalls)
	return role, contentLen, toolCalls, reasoningLen, preview
}

// ---------------------------------------------------------------------------
// Markdown rendering
// ---------------------------------------------------------------------------

var (
	agentMarkdownRenderer     *glamour.TermRenderer
	agentMarkdownRendererErr  error
	agentMarkdownRendererOnce sync.Once
)

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
	rendered = strings.TrimSpace(trimRenderedMarkdownLineEnds(rendered))
	if rendered == "" {
		return content
	}
	return rendered
}

func getAgentMarkdownRenderer() (*glamour.TermRenderer, error) {
	agentMarkdownRendererOnce.Do(func() {
		opts := []glamour.TermRendererOption{
			glamour.WithAutoStyle(),
			// Auto-detect the richest profile the terminal advertises (truecolor
			// -> 256 -> ANSI) instead of pinning 16-color ANSI, so markdown answers
			// render with real depth on modern terminals.
			glamour.WithColorProfile(termenv.ColorProfile()),
			glamour.WithEmoji(),
		}
		if w := terminalWidth(); w > 0 {
			opts = append(opts, glamour.WithWordWrap(w))
		}
		agentMarkdownRenderer, agentMarkdownRendererErr = glamour.NewTermRenderer(opts...)
	})
	return agentMarkdownRenderer, agentMarkdownRendererErr
}

// terminalWidth returns the stdout column count, or 0 when unknown (piped /
// forwarded sessions) so the markdown renderer skips width-bounded wrapping.
func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 0
}

// trimRenderedMarkdownLineEnds strips trailing visible whitespace from each
// line while preserving ANSI escape sequences that follow the last visible
// character. This avoids the "invisible trailing spaces" artifact from glamour
// padding without clobbering reset sequences that close styled spans.
func trimRenderedMarkdownLineEnds(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	start := 0
	for start < len(s) {
		rel := strings.IndexByte(s[start:], '\n')
		if rel < 0 {
			b.WriteString(trimANSIVisibleRight(s[start:]))
			break
		}
		end := start + rel
		b.WriteString(trimANSIVisibleRight(s[start:end]))
		b.WriteByte('\n')
		start = end + 1
	}
	return b.String()
}

func trimANSIVisibleRight(line string) string {
	cut := 0
	extendCutWithANSI := false
	for i := 0; i < len(line); {
		if end, ok := ansiEscapeEnd(line, i); ok {
			if extendCutWithANSI && ansiClosesStyle(line[i:end]) {
				cut = end
			}
			i = end
			continue
		}

		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 1 {
			cut = i + size
			extendCutWithANSI = true
			i += size
			continue
		}

		end := i + size
		if unicode.IsSpace(r) {
			extendCutWithANSI = false
		} else {
			cut = end
			extendCutWithANSI = true
		}
		i = end
	}
	return line[:cut]
}

func ansiClosesStyle(seq string) bool {
	if strings.HasPrefix(seq, "\x1b]8;;") {
		return true
	}
	if len(seq) < 3 || seq[0] != '\x1b' || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return false
	}
	params := seq[2 : len(seq)-1]
	if params == "" {
		return true
	}
	for _, param := range strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' }) {
		switch param {
		case "0", "22", "23", "24", "25", "27", "28", "29", "39", "49", "59":
			return true
		}
	}
	return false
}

func ansiEscapeEnd(s string, start int) (int, bool) {
	if start >= len(s) || s[start] != '\x1b' {
		return 0, false
	}
	if start+1 >= len(s) {
		return start + 1, true
	}

	switch s[start+1] {
	case '[':
		for i := start + 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1, true
			}
		}
		return len(s), true
	case ']':
		for i := start + 2; i < len(s); i++ {
			switch {
			case s[i] == '\a':
				return i + 1, true
			case s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\':
				return i + 2, true
			}
		}
		return len(s), true
	default:
		return start + 2, true
	}
}

// ---------------------------------------------------------------------------
// Tool argument summarisation
// ---------------------------------------------------------------------------

func summarizeToolArguments(name, arguments string) string {
	args := decodeToolArguments(arguments)
	if len(args) == 0 {
		return ""
	}
	switch name {
	case "bash":
		return truncate.Clip(stringArg(args, "command"), agentStatusPreviewLimit)
	case "read":
		path := stringArg(args, "path")
		if offset := stringArg(args, "offset"); offset != "" && offset != "0" {
			path += fmt.Sprintf(" (offset=%s)", offset)
		}
		return truncate.Clip(path, agentStatusPreviewLimit)
	case "write":
		path := stringArg(args, "path")
		if edits, ok := args["edits"]; ok && edits != nil {
			if arr, ok := edits.([]any); ok {
				path += fmt.Sprintf(" (edit: %d change(s))", len(arr))
			}
		}
		return truncate.Clip(path, agentStatusPreviewLimit)
	case "glob":
		return truncate.Clip(joinAgentSummaryParts(
			stringArg(args, "pattern"),
			prefixedArg("in ", stringArg(args, "path")),
		), agentStatusPreviewLimit)
	case "subagent":
		action := stringArg(args, "action")
		if action == "" || action == "create" {
			mode := stringArg(args, "mode")
			typeName := stringArg(args, "type")
			prompt := truncate.Clip(stringArg(args, "prompt"), 80)
			return joinAgentSummaryParts(typeName, prefixedArg("mode=", mode), prompt)
		}
		return joinAgentSummaryParts(action, stringArg(args, "name"))
	case "ioa_space":
		return truncate.Clip(stringArg(args, "name"), agentStatusPreviewLimit)
	case "ioa_send":
		return truncate.Clip(prefixedArg("space ", stringArg(args, "space_id")), agentStatusPreviewLimit)
	case "ioa_read":
		return truncate.Clip(joinAgentSummaryParts(
			prefixedArg("space ", stringArg(args, "space_id")),
			prefixedArg("message ", stringArg(args, "message_id")),
			prefixedArg("after ", stringArg(args, "after")),
		), agentStatusPreviewLimit)
	default:
		return truncate.Clip(firstNonEmptyArg(args, "target", "url", "input", "path", "name"), agentStatusPreviewLimit)
	}
}

// ---------------------------------------------------------------------------
// Structured tool argument formatting (verbose mode)
// ---------------------------------------------------------------------------

type toolArgLine struct {
	key   string
	value string
}

func formatToolArguments(name, arguments string) []toolArgLine {
	args := decodeToolArguments(arguments)
	if len(args) == 0 {
		return nil
	}
	switch name {
	case "bash":
		return collectArgs(args, "command")
	case "read":
		return collectArgs(args, "path", "offset", "limit")
	case "write":
		return collectWriteArgs(args)
	case "glob":
		return collectArgs(args, "pattern", "path")
	case "fetch":
		return collectArgs(args, "url", "extract")
	case "web_search":
		return collectArgs(args, "query", "num")
	case "subagent":
		return collectSubagentArgs(args)
	case "ioa_space":
		return collectArgs(args, "name")
	case "ioa_send":
		return collectArgs(args, "space_id", "message")
	case "ioa_read":
		return collectArgs(args, "space_id", "message_id", "after")
	default:
		return collectAllArgs(args)
	}
}

func collectArgs(args map[string]any, keys ...string) []toolArgLine {
	var lines []toolArgLine
	for _, k := range keys {
		v := stringArg(args, k)
		if v == "" || v == "0" {
			continue
		}
		lines = append(lines, toolArgLine{key: k, value: truncate.Clip(v, agentStatusPreviewLimit)})
	}
	return lines
}

func collectWriteArgs(args map[string]any) []toolArgLine {
	var lines []toolArgLine
	if p := stringArg(args, "path"); p != "" {
		lines = append(lines, toolArgLine{key: "path", value: p})
	}
	if edits, ok := args["edits"]; ok && edits != nil {
		if arr, ok := edits.([]any); ok {
			lines = append(lines, toolArgLine{key: "edits", value: fmt.Sprintf("%d change(s)", len(arr))})
		}
	} else if content := stringArg(args, "content"); content != "" {
		lines = append(lines, toolArgLine{key: "content", value: fmt.Sprintf("%d bytes", len(content))})
	}
	return lines
}

func collectSubagentArgs(args map[string]any) []toolArgLine {
	var lines []toolArgLine
	for _, k := range []string{"action", "type", "mode", "name"} {
		if v := stringArg(args, k); v != "" {
			lines = append(lines, toolArgLine{key: k, value: v})
		}
	}
	if p := stringArg(args, "prompt"); p != "" {
		lines = append(lines, toolArgLine{key: "prompt", value: truncate.Clip(p, 80)})
	}
	return lines
}

func collectAllArgs(args map[string]any) []toolArgLine {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []toolArgLine
	for _, k := range keys {
		v := stringArg(args, k)
		if v == "" {
			continue
		}
		lines = append(lines, toolArgLine{key: k, value: truncate.Clip(v, agentStatusPreviewLimit)})
	}
	return lines
}

// ---------------------------------------------------------------------------
// JSON / string helpers
// ---------------------------------------------------------------------------

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

func compactAgentJSON(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(value)); err == nil {
		value = buf.String()
	}
	return truncate.Clip(value, limit)
}
