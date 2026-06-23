package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

type timelineItem interface {
	writeMarkdown(sb *strings.Builder, ctx *renderContext)
}

type TimelineEntry struct {
	Timestamp time.Time
	Type      string
	Data      timelineItem
}

type renderContext struct {
	startTS time.Time
}

// ---------------------------------------------------------------------------
// Parse
// ---------------------------------------------------------------------------

func ParseTimelineFile(path string) ([]TimelineEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []TimelineEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if e, ok := parseLine(line); ok {
			entries = append(entries, e)
		}
	}
	return entries, scanner.Err()
}

func parseLine(line []byte) (TimelineEntry, bool) {
	rec, err := ParseRecord(line)
	if err != nil || rec.Type == "" {
		return TimelineEntry{}, false
	}
	if item := parseRecordData(rec); item != nil {
		return TimelineEntry{Timestamp: rec.Timestamp, Type: string(rec.Type), Data: item}, true
	}
	return TimelineEntry{}, false
}

func parseRecordData(rec Record) timelineItem {
	if rec.Loot {
		return unmarshalItem[Loot](rec.Data)
	}
	switch rec.Type {
	case TypeScanStart:
		return unmarshalItem[ScanStart](rec.Data)
	case TypeGogo:
		return unmarshalItem[serviceView](rec.Data)
	case TypeSpray:
		return unmarshalItem[webView](rec.Data)
	case TypeAgent:
		return unmarshalItem[AgentEvent](rec.Data)
	case TypeScanEnd:
		return unmarshalItem[ScanEnd](rec.Data)
	}
	return nil
}

func unmarshalItem[T any](data json.RawMessage) *T {
	var v T
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	return &v
}

// ---------------------------------------------------------------------------
// Render entry points
// ---------------------------------------------------------------------------

func RenderTimeline(w io.Writer, entries []TimelineEntry) error {
	_, err := io.WriteString(w, renderMD(BuildTimelineMarkdown(entries)))
	return err
}

func RenderTimelineMarkdown(w io.Writer, entries []TimelineEntry) error {
	_, err := io.WriteString(w, BuildTimelineMarkdown(entries))
	return err
}

func BuildTimelineMarkdown(entries []TimelineEntry) string {
	var sb strings.Builder
	sess := collectSessionMeta(entries)
	writeHeader(&sb, &sess)

	ctx := &renderContext{startTS: sess.startTS}
	for _, e := range entries {
		e.Data.writeMarkdown(&sb, ctx)
	}
	return sb.String()
}

func writeHeader(sb *strings.Builder, sess *sessionMeta) {
	if sess.id == "" && sess.model == "" {
		return
	}
	label := shortID(sess.id)
	if sess.parentID != "" {
		label += " ← " + shortID(sess.parentID)
	}
	if label != "" {
		sb.WriteString(fmt.Sprintf("# Agent `%s`\n\n", label))
	}
	var meta []string
	if sess.model != "" {
		meta = append(meta, fmt.Sprintf("**model:** %s", sess.model))
	}
	if d := sess.duration(); d > 0 {
		meta = append(meta, fmt.Sprintf("**duration:** %s", fmtDuration(d)))
	}
	if sess.totalTokens > 0 {
		meta = append(meta, fmt.Sprintf("**tokens:** %d", sess.totalTokens))
	}
	if sess.stop != "" {
		meta = append(meta, fmt.Sprintf("**status:** %s", sess.stop))
	}
	if len(meta) > 0 {
		sb.WriteString("> " + strings.Join(meta, " · ") + "\n\n")
	}
}

// ---------------------------------------------------------------------------
// AgentEvent implements timelineItem
// ---------------------------------------------------------------------------

type AgentEvent struct {
	Type            string           `json:"type"`
	SessionID       string           `json:"session_id"`
	ParentSessionID string           `json:"parent_session_id"`
	Turn            int              `json:"turn"`
	ToolCallID      string           `json:"tool_call_id"`
	ToolName        string           `json:"tool_name"`
	Arguments       string           `json:"arguments"`
	Result          string           `json:"result"`
	IsError         bool             `json:"is_error"`
	Error           string           `json:"error"`
	Stop            string           `json:"stop"`
	Message         *AgentEventMsg   `json:"message"`
	ToolResults     []AgentEventMsg  `json:"tool_results"`
	Usage           *AgentEventUsage `json:"usage"`
	ContextTokens   int              `json:"context_tokens"`
	NewMessages     int              `json:"new_messages"`
	RequestModel    string           `json:"request_model"`
	RequestMessages int              `json:"request_messages"`
	RequestTools    int              `json:"request_tools"`
}

type AgentEventMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []agentToolCall `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type AgentEventUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type agentToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (ev *AgentEvent) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	switch ev.Type {
	case "turn_start":
		sb.WriteString(fmt.Sprintf("## Turn %d\n\n", ev.Turn))

	case "message_end":
		if ev.Message == nil {
			return
		}
		switch ev.Message.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("> %s\n\n", TruncateStr(ev.Message.Content, 200)))
		case "assistant":
			if len(ev.Message.ToolCalls) > 0 {
				return
			}
			if ev.Message.Content != "" {
				sb.WriteString(ev.Message.Content + "\n\n")
			}
		}

	case "tool_execution_start":
		args := summarizeToolArgs(ev.ToolName, ev.Arguments)
		if args != "" {
			sb.WriteString(fmt.Sprintf("- **%s** `%s`\n", ev.ToolName, args))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", ev.ToolName))
		}

	case "tool_execution_end":
		if ev.IsError || ev.Error != "" {
			errMsg := ev.Error
			if errMsg == "" {
				errMsg = TruncateStr(ev.Result, 120)
			}
			sb.WriteString(fmt.Sprintf("  - ✗ `%s`\n", TruncateStr(errMsg, 120)))
		} else {
			sb.WriteString(fmt.Sprintf("  - ✓ %s\n", compactResult(ev.Result, 150)))
		}

	case "turn_end":
		if ev.Usage != nil && ev.Usage.TotalTokens > 0 {
			usage := fmt.Sprintf("*%d tokens", ev.Usage.TotalTokens)
			if ev.Usage.CacheReadTokens > 0 && ev.Usage.PromptTokens > 0 {
				pct := float64(ev.Usage.CacheReadTokens) / float64(ev.Usage.PromptTokens) * 100
				usage += fmt.Sprintf(", cache %.0f%%", pct)
			}
			sb.WriteString("\n" + usage + "*\n")
		}
		sb.WriteString("\n")
	}
}

// ---------------------------------------------------------------------------
// Scan types implement timelineItem
// ---------------------------------------------------------------------------

func (d *ScanStart) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	sb.WriteString(fmt.Sprintf("- **scan** targets=%s mode=%s\n", strings.Join(d.Targets, ", "), d.Mode))
}

func (s *serviceView) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	line := fmt.Sprintf("  - **service** `%s`", s.displayTarget())
	if s.Protocol != "" {
		line += " " + s.Protocol
	}
	if b := s.displayBanner(); b != "" {
		line += " — " + TruncateStr(b, 60)
	}
	sb.WriteString(line + "\n")
}

func (w *webView) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	fingers := ""
	if names := w.fingerNames(); len(names) > 0 {
		fingers = " [" + strings.Join(names, ", ") + "]"
	}
	sb.WriteString(fmt.Sprintf("  - **web** `%s` %d %s%s\n", w.URL, w.Status, w.Title, fingers))
}

func (l *Loot) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	sb.WriteString(fmt.Sprintf("  - **%s** `%s` %s\n", l.Kind, l.Target, l.Description))
}

func (d *ScanEnd) writeMarkdown(sb *strings.Builder, _ *renderContext) {
	sb.WriteString(fmt.Sprintf("\n> **scan done** %.1fs — %d services, %d webs, %d loots\n\n",
		d.Duration, d.Services, d.Webs, d.Loots))
}

// ---------------------------------------------------------------------------
// Session metadata
// ---------------------------------------------------------------------------

type sessionMeta struct {
	id, parentID, model, stop string
	turns, totalTokens        int
	startTS, endTS            time.Time
}

func (s *sessionMeta) duration() time.Duration {
	if s.startTS.IsZero() || s.endTS.IsZero() {
		return 0
	}
	return s.endTS.Sub(s.startTS)
}

func collectSessionMeta(entries []TimelineEntry) sessionMeta {
	var m sessionMeta
	for _, e := range entries {
		ev, ok := e.Data.(*AgentEvent)
		if !ok {
			continue
		}
		if m.id == "" {
			m.id = ev.SessionID
			m.parentID = ev.ParentSessionID
		}
		if ev.RequestModel != "" && m.model == "" {
			m.model = ev.RequestModel
		}
		switch ev.Type {
		case "agent_start":
			m.startTS = e.Timestamp
		case "agent_end":
			m.endTS = e.Timestamp
			m.stop = ev.Stop
		case "turn_start":
			m.turns++
		case "turn_end":
			if ev.Usage != nil {
				m.totalTokens = ev.Usage.TotalTokens
			}
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// glamour renderer
// ---------------------------------------------------------------------------

var (
	timelineRenderer     *glamour.TermRenderer
	timelineRendererErr  error
	timelineRendererOnce sync.Once
)

func getTimelineRenderer() (*glamour.TermRenderer, error) {
	timelineRendererOnce.Do(func() {
		timelineRenderer, timelineRendererErr = glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithColorProfile(termenv.ANSI),
			glamour.WithEmoji(),
			glamour.WithWordWrap(120),
		)
	})
	return timelineRenderer, timelineRendererErr
}

func renderMD(md string) string {
	r, err := getTimelineRenderer()
	if err != nil {
		return md
	}
	rendered, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.TrimRight(rendered, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func summarizeToolArgs(name, arguments string) string {
	if arguments == "" {
		return ""
	}
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return TruncateStr(arguments, 80)
	}
	switch name {
	case "bash", "scan", "gogo", "spray", "zombie", "neutron", "katana", "passive":
		if cmd, ok := args["command"].(string); ok {
			return TruncateStr(cmd, 120)
		}
	case "read":
		return stringVal(args, "path")
	case "write":
		path := stringVal(args, "path")
		if edits, ok := args["edits"]; ok {
			if arr, ok := edits.([]any); ok {
				return fmt.Sprintf("%s (%d edits)", path, len(arr))
			}
		}
		return path
	case "glob":
		return strings.Join(CompactStrings(stringVal(args, "pattern"), stringVal(args, "path")), " in ")
	case "subagent":
		mode := stringVal(args, "mode")
		prompt := TruncateStr(stringVal(args, "prompt"), 60)
		if mode != "" {
			return mode + ": " + prompt
		}
		return prompt
	}
	return TruncateStr(arguments, 80)
}

func stringVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func compactResult(result string, maxLen int) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return "(empty)"
	}
	lines := strings.Split(result, "\n")
	if len(lines) == 1 {
		return TruncateStr(result, maxLen)
	}
	first := strings.TrimSpace(lines[0])
	return TruncateStr(first, maxLen-20) + fmt.Sprintf(" (+%d lines)", len(lines)-1)
}
