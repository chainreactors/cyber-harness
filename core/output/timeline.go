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

	"github.com/chainreactors/aiscan/core/aop"
	xcommand "github.com/chainreactors/aiscan/core/aop/x/command"
	"github.com/chainreactors/utils/parsers"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

type TimelineEntry struct {
	Timestamp time.Time
	Type      string
	Data      any
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
	var event aop.Event
	if json.Unmarshal(line, &event) == nil && event.Valid() {
		timestamp, err := time.Parse(time.RFC3339Nano, event.TS)
		if err == nil {
			return TimelineEntry{Timestamp: timestamp, Type: event.Type, Data: &event}, true
		}
	}
	rec, err := ParseRecord(line)
	if err != nil || rec.Type == "" {
		return TimelineEntry{}, false
	}
	if item := parseRecordData(rec); item != nil {
		return TimelineEntry{Timestamp: rec.Timestamp, Type: string(rec.Type), Data: item}, true
	}
	return TimelineEntry{}, false
}

func parseRecordData(rec Record) any {
	if rec.Loot {
		return unmarshalItem[parsers.Loot](rec.Data)
	}
	switch rec.Type {
	case TypeScanStart:
		return unmarshalItem[ScanStart](rec.Data)
	case TypeGogo:
		return unmarshalItem[parsers.GOGOResult](rec.Data)
	case TypeSpray:
		return unmarshalItem[parsers.SprayResult](rec.Data)
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

	for _, e := range entries {
		switch d := e.Data.(type) {
		case *ScanStart:
			d.writeMarkdown(&sb)
		case *parsers.GOGOResult:
			writeGogoMarkdown(&sb, d)
		case *parsers.SprayResult:
			writeSprayMarkdown(&sb, d)
		case *parsers.Loot:
			writeLootMarkdown(&sb, d)
		case *aop.Event:
			writeAOPMarkdown(&sb, d)
		case *ScanEnd:
			d.writeMarkdown(&sb)
		}
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
// Scan types
// ---------------------------------------------------------------------------

func (d *ScanStart) writeMarkdown(sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf("- **scan** targets=%s mode=%s\n", strings.Join(d.Targets, ", "), d.Mode))
}

func (d *ScanEnd) writeMarkdown(sb *strings.Builder) {
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
		switch d := e.Data.(type) {
		case *aop.Event:
			if m.id == "" {
				m.id = d.SessionID
			}
			switch d.Type {
			case aop.TypeSessionStart:
				m.startTS = e.Timestamp
				if data, err := aop.DecodeData[aop.SessionStartData](*d); err == nil {
					m.parentID = data.ParentSessionID
					if data.Model != "" && m.model == "" {
						m.model = data.Model
					}
				}
			case aop.TypeSessionEnd:
				m.endTS = e.Timestamp
			case aop.TypeTurnStart:
				m.turns++
			case aop.TypeTurnEnd:
				m.endTS = e.Timestamp
				if data, err := aop.DecodeData[aop.TurnEndData](*d); err == nil {
					m.stop = data.Stop
					if data.Usage != nil && data.Usage.TotalTokens > 0 {
						m.totalTokens = data.Usage.TotalTokens
					}
				}
			case aop.TypeUsage:
				if data, err := aop.DecodeData[aop.UsageData](*d); err == nil && data.TotalTokens > 0 {
					m.totalTokens = data.TotalTokens
				}
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
	case "bash", "scan", "gogo", "spray", "zombie", "neutron", "proton", "katana", "passive":
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
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%v", v)
	default:
		return ""
	}
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

func writeAOPMarkdown(sb *strings.Builder, event *aop.Event) {
	if event == nil {
		return
	}
	switch event.Type {
	case aop.TypeTurnStart:
		sb.WriteString(fmt.Sprintf("## Run %s\n\n", event.TurnID))

	case aop.TypeMessage:
		data, err := aop.DecodeData[aop.MessageData](*event)
		if err != nil {
			return
		}
		var textParts []string
		for _, part := range data.Parts {
			if part.Type == aop.PartText && part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		text := strings.Join(textParts, "\n")
		if text == "" {
			return
		}
		if data.Role == "user" {
			sb.WriteString(fmt.Sprintf("> %s\n\n", TruncateStr(text, 200)))
		} else {
			detail, ok, _ := xcommand.GetDetail(*event)
			if ok && detail.Presentation == "preformatted" {
				sb.WriteString(markdownCodeFence(text) + "\n\n")
			} else {
				sb.WriteString(text + "\n\n")
			}
		}

	case aop.TypeToolCall:
		data, err := aop.DecodeData[aop.ToolCallData](*event)
		if err != nil {
			return
		}
		argsStr := ""
		switch args := data.Args.(type) {
		case string:
			argsStr = args
		case map[string]any:
			raw, _ := json.Marshal(args)
			argsStr = string(raw)
		}
		summary := summarizeToolArgs(data.ToolName, argsStr)
		if summary != "" {
			sb.WriteString(fmt.Sprintf("- **%s** `%s`\n", data.ToolName, summary))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", data.ToolName))
		}

	case aop.TypeToolResult:
		data, err := aop.DecodeData[aop.ToolResultData](*event)
		if err != nil {
			return
		}
		result := aop.ToolResultText(data.Content)
		if data.IsError {
			sb.WriteString(fmt.Sprintf("  - ✗ `%s`\n", TruncateStr(result, 120)))
		} else {
			sb.WriteString(fmt.Sprintf("  - ✓ %s\n", compactResult(result, 150)))
		}

	case aop.TypeUsage:
		data, err := aop.DecodeData[aop.UsageData](*event)
		if err != nil {
			return
		}
		if data.TotalTokens > 0 {
			usage := fmt.Sprintf("*%d tokens", data.TotalTokens)
			if data.CacheReadTokens > 0 && data.InputTokens > 0 {
				pct := float64(data.CacheReadTokens) / float64(data.InputTokens) * 100
				usage += fmt.Sprintf(", cache %.0f%%", pct)
			}
			sb.WriteString("\n" + usage + "*\n\n")
		}

	case aop.TypeError:
		data, err := aop.DecodeData[aop.ErrorData](*event)
		if err == nil && data.Message != "" {
			sb.WriteString(fmt.Sprintf("\n> **error:** %s\n\n", data.Message))
		}

	case aop.TypeTurnEnd:
		data, err := aop.DecodeData[aop.TurnEndData](*event)
		if err == nil {
			sb.WriteString(fmt.Sprintf("\n> **run done** (stop=%s)\n\n", data.Stop))
		}

	case aop.TypeSessionEnd:
		data, err := aop.DecodeData[aop.SessionEndData](*event)
		if err == nil {
			sb.WriteString(fmt.Sprintf("\n> **session closed** (reason=%s)\n\n", data.Reason))
		}
	}
}

func markdownCodeFence(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + text + "\n" + fence
}
