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

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
	"google.golang.org/protobuf/encoding/protojson"
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
	event := new(aop.Event)
	if protojson.Unmarshal(line, event) == nil && event.SessionId != "" && event.Payload != nil {
		timestamp := time.Time{}
		if event.EmittedAt != nil {
			timestamp = event.EmittedAt.AsTime()
		}
		return TimelineEntry{Timestamp: timestamp, Type: aop.Kind(event), Data: event}, true
	}
	return TimelineEntry{}, false
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

// RenderFile renders an AOP Event ProtoJSONL file. Scanner fact files are
// libcstx SCO JSONL and are intentionally not accepted as agent timelines.
func RenderFile(path, format, outputPath string) error {
	var writer io.Writer = os.Stdout
	if outputPath != "" {
		file, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer file.Close()
		writer = file
	}
	entries, err := ParseTimelineFile(path)
	if err != nil {
		return err
	}
	if strings.EqualFold(format, "markdown") || strings.EqualFold(format, "md") {
		return RenderTimelineMarkdown(writer, entries)
	}
	return RenderTimeline(writer, entries)
}

func BuildTimelineMarkdown(entries []TimelineEntry) string {
	var sb strings.Builder
	sess := collectSessionMeta(entries)
	writeHeader(&sb, &sess)

	for _, e := range entries {
		switch d := e.Data.(type) {
		case *aop.Event:
			writeAOPMarkdown(&sb, d)
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
				m.id = d.SessionId
			}
			switch payload := d.Payload.(type) {
			case *aop.Event_SessionStarted:
				m.startTS = e.Timestamp
				m.parentID = payload.SessionStarted.ParentSessionId
				if payload.SessionStarted.Model != "" && m.model == "" {
					m.model = payload.SessionStarted.Model
				}
			case *aop.Event_SessionEnded:
				m.endTS = e.Timestamp
			case *aop.Event_TurnStarted:
				m.turns++
			case *aop.Event_TurnEnded:
				m.endTS = e.Timestamp
				m.stop = payload.TurnEnded.StopReason
				if payload.TurnEnded.Usage != nil && payload.TurnEnded.Usage.TotalTokens > 0 {
					m.totalTokens = int(payload.TurnEnded.Usage.TotalTokens)
				}
			case *aop.Event_Usage:
				if payload.Usage.TotalTokens > 0 {
					m.totalTokens = int(payload.Usage.TotalTokens)
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
	switch payload := event.Payload.(type) {
	case *aop.Event_TurnStarted:
		sb.WriteString(fmt.Sprintf("## Run %s\n\n", event.TurnId))

	case *aop.Event_Message:
		data := payload.Message
		var textParts []string
		for _, part := range data.Content {
			if text := part.GetText().GetText(); text != "" {
				textParts = append(textParts, text)
			}
		}
		text := strings.Join(textParts, "\n")
		if text == "" {
			return
		}
		if data.Role == "user" {
			sb.WriteString(fmt.Sprintf("> %s\n\n", TruncateStr(text, 200)))
		} else {
			detail, ok, _ := types.GetCommandDetail(event)
			if ok && detail.Presentation == "preformatted" {
				sb.WriteString(markdownCodeFence(text) + "\n\n")
			} else {
				sb.WriteString(text + "\n\n")
			}
		}

	case *aop.Event_ToolCall:
		data := payload.ToolCall
		argsStr := string(data.GetArguments().GetData())
		summary := summarizeToolArgs(data.Name, argsStr)
		if summary != "" {
			sb.WriteString(fmt.Sprintf("- **%s** `%s`\n", data.Name, summary))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", data.Name))
		}

	case *aop.Event_ToolResult:
		data := payload.ToolResult
		result := aopContentText(data.Output)
		if data.IsError {
			sb.WriteString(fmt.Sprintf("  - ✗ `%s`\n", TruncateStr(result, 120)))
		} else {
			sb.WriteString(fmt.Sprintf("  - ✓ %s\n", compactResult(result, 150)))
		}

	case *aop.Event_Usage:
		data := payload.Usage
		if data.TotalTokens > 0 {
			usage := fmt.Sprintf("*%d tokens", data.TotalTokens)
			if data.Detail["cache_read"] > 0 && data.InputTokens > 0 {
				pct := float64(data.Detail["cache_read"]) / float64(data.InputTokens) * 100
				usage += fmt.Sprintf(", cache %.0f%%", pct)
			}
			sb.WriteString("\n" + usage + "*\n\n")
		}

	case *aop.Event_Error:
		if payload.Error.Message != "" {
			sb.WriteString(fmt.Sprintf("\n> **error:** %s\n\n", payload.Error.Message))
		}

	case *aop.Event_TurnEnded:
		sb.WriteString(fmt.Sprintf("\n> **run done** (stop=%s)\n\n", payload.TurnEnded.StopReason))

	case *aop.Event_SessionEnded:
		sb.WriteString(fmt.Sprintf("\n> **session closed** (reason=%s)\n\n", payload.SessionEnded.Reason))
	}
}

func aopContentText(content []*aop.Content) string {
	var parts []string
	for _, item := range content {
		if text := item.GetText().GetText(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func markdownCodeFence(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + text + "\n" + fence
}
