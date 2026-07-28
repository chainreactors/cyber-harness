package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/truncate"
	"github.com/chainreactors/aiscan/core/util"
)

const (
	liveStatusWidth    = len(liveStatusThinking)
	liveStatusThinking = "thinking"
	liveStatusTooling  = "tooling"
	liveStatusTalking  = "talking"
	inboxPreviewItems  = 3
	inboxPreviewRunes  = 64
)

// toolEvent is the TUI's merged view of one AOP tool.call/tool.result pair.
type toolEvent struct {
	id        string
	name      string
	args      string
	result    string
	isError   bool
	done      bool
	startedAt time.Time
	elapsed   time.Duration
}

type LiveStatus struct {
	view *LiveView

	status string
	note   string

	turn           int
	turnToolCalls  int
	turnUsage      *agent.Usage
	outputEstimate int
	contextTokens  int
	contextWindow  int
	showUsage      bool

	tools map[string]*toolEvent
	order []string
	inbox []string

	dim            func(string) string
	renderToolLine func(*toolEvent) string
}

func (l *LiveStatus) SetUsageVisible(visible bool) {
	if l != nil {
		l.showUsage = visible
	}
}

func NewLiveStatus(view *LiveView, dim func(string) string, renderToolLine func(*toolEvent) string) *LiveStatus {
	if dim == nil {
		dim = func(s string) string { return s }
	}
	if renderToolLine == nil {
		renderToolLine = func(*toolEvent) string { return "" }
	}
	return &LiveStatus{
		view:           view,
		status:         liveStatusThinking,
		showUsage:      true,
		tools:          make(map[string]*toolEvent),
		dim:            dim,
		renderToolLine: renderToolLine,
	}
}

func (l *LiveStatus) SetContextWindow(tokens int) {
	if l == nil {
		return
	}
	l.contextWindow = tokens
}

func (l *LiveStatus) Reset() {
	if l == nil {
		return
	}
	l.Stop()
	l.status = liveStatusThinking
	l.note = ""
	l.turn = 0
	l.turnToolCalls = 0
	l.turnUsage = nil
	l.outputEstimate = 0
	l.contextTokens = 0
	l.tools = make(map[string]*toolEvent)
	l.order = nil
}

func (l *LiveStatus) BeginTurn(turn int) {
	if l == nil {
		return
	}
	l.status = liveStatusThinking
	l.note = ""
	l.turn = turn
	l.turnToolCalls = 0
	l.turnUsage = nil
	l.outputEstimate = 0
	l.clearTools()
	l.view.SetElapsedStart(time.Now())
	l.Render()
}

// NoteDelta switches the shared status row from thinking to talking once
// assistant response text begins. It remains the same transient composer row;
// response text itself is committed separately to terminal scrollback.
func (l *LiveStatus) NoteDelta(textDelta bool) {
	if l == nil {
		return
	}
	if textDelta && !l.HasTools() && l.status != liveStatusTalking {
		l.status = liveStatusTalking
		l.note = ""
		l.Render()
	}
}

func (l *LiveStatus) SetTurnUsage(usage agent.Usage) {
	if l == nil {
		return
	}
	l.turnUsage = &usage
}

func (l *LiveStatus) SetOutputEstimate(tokens int) {
	if l == nil {
		return
	}
	if tokens < 0 {
		tokens = 0
	}
	l.outputEstimate = tokens
	if l.view != nil {
		// The animation timer performs the actual redraw. Keeping its pending
		// lines current makes token changes visible at the configured cadence
		// without repainting once per provider delta.
		l.view.UpdateDeferred(l.lines())
	}
}

func (l *LiveStatus) SetTurnToolCalls(count int) {
	if l == nil {
		return
	}
	l.turnToolCalls = count
}

// SetInbox updates the pending user input preview. The inbox intentionally
// survives Reset so a queued run remains visible while the next run starts.
func (l *LiveStatus) SetInbox(items []string, render bool) {
	if l == nil {
		return
	}
	l.inbox = append(l.inbox[:0], items...)
	if render {
		l.Render()
	}
}

func (l *LiveStatus) ShowEvalRound(round int) {
	if l == nil {
		return
	}
	l.status = liveStatusTooling
	l.note = fmt.Sprintf("eval · round %d", round)
	l.clearTools()
	l.Render()
}

func (l *LiveStatus) StartTool(ev *toolEvent) {
	if l == nil || ev == nil {
		return
	}
	l.status = liveStatusTooling
	l.note = ""
	if ev.id != "" {
		l.ensureTools()
		if !l.hasTool(ev.id) {
			l.order = append(l.order, ev.id)
		}
		l.tools[ev.id] = ev
	}
	l.Render()
}

func (l *LiveStatus) UpdateTool(ev *toolEvent) (tracked bool, done bool) {
	if l == nil || ev == nil || ev.id == "" || !l.hasTool(ev.id) {
		return false, false
	}
	l.status = liveStatusTooling
	l.note = ""
	l.ensureTools()
	// A tool.result event carries only id/name/result — inherit the call-side
	// metadata (args, start time) so the rendered line keeps its context.
	if prev := l.tools[ev.id]; prev != nil {
		if ev.name == "" {
			ev.name = prev.name
		}
		if ev.args == "" {
			ev.args = prev.args
		}
		if ev.startedAt.IsZero() {
			ev.startedAt = prev.startedAt
		}
	}
	l.tools[ev.id] = ev
	if l.allToolsDone() {
		return true, true
	}
	l.Render()
	return true, false
}

// FinishTurn records the latest context size. Per-turn statistics remain in
// the transient status line and are intentionally not committed to history.
func (l *LiveStatus) FinishTurn(contextTokens int) {
	if l == nil {
		return
	}
	if contextTokens > 0 {
		l.contextTokens = contextTokens
	} else if l.turnUsage != nil && l.turnUsage.PromptTokens > 0 {
		l.contextTokens = l.turnUsage.PromptTokens
	}
	l.turnUsage = nil
}

func (l *LiveStatus) HasTools() bool {
	return l != nil && len(l.order) > 0
}

func (l *LiveStatus) Status() string {
	if l == nil || l.status == "" {
		return liveStatusThinking
	}
	return l.status
}

func (l *LiveStatus) Running() bool {
	if l == nil || l.view == nil {
		return false
	}
	l.view.mu.Lock()
	defer l.view.mu.Unlock()
	return l.view.running
}

func (l *LiveStatus) WithHidden(fn func()) {
	if l == nil || l.view == nil {
		if fn != nil {
			fn()
		}
		return
	}
	l.view.WithHidden(fn)
}

func (l *LiveStatus) Stop() {
	if l == nil || l.view == nil {
		return
	}
	l.view.Stop()
}

func (l *LiveStatus) StopAndDrainTools() []*toolEvent {
	if l == nil {
		return nil
	}
	l.Stop()
	return l.DrainTools()
}

func (l *LiveStatus) DrainTools() []*toolEvent {
	if l == nil || len(l.order) == 0 {
		return nil
	}
	events := make([]*toolEvent, 0, len(l.order))
	for _, id := range l.order {
		if event, ok := l.tools[id]; ok {
			events = append(events, event)
			delete(l.tools, id)
		}
	}
	l.order = nil
	return events
}

func (l *LiveStatus) Render() {
	if l == nil || l.view == nil {
		return
	}
	l.view.Update(l.lines())
	l.view.Start()
}

func (l *LiveStatus) lines() []string {
	lines := []string{l.statusLine()}
	if l.Status() == liveStatusTooling && len(l.order) > 0 {
		lines = append(lines, l.toolLines()...)
	}
	return lines
}

func (l *LiveStatus) statusLine() string {
	line := spinnerSentinel + " " + fmt.Sprintf("%-*s", liveStatusWidth, l.Status())
	var details []string
	if turn := l.formatTurnDetails(); turn != "" {
		details = append(details, l.dim(turn))
	}
	if l.note != "" {
		details = append(details, l.dim(l.note))
	}
	if inbox := l.formatInbox(); inbox != "" {
		details = append(details, l.dim(inbox))
	}
	if len(details) > 0 {
		line += " · " + strings.Join(details, " · ")
	}
	return line
}

func (l *LiveStatus) formatInbox() string {
	if l == nil || len(l.inbox) == 0 {
		return ""
	}
	count := len(l.inbox)
	limit := count
	if limit > inboxPreviewItems {
		limit = inboxPreviewItems
	}
	previews := make([]string, 0, limit+1)
	for _, item := range l.inbox[:limit] {
		item = strings.Join(strings.Fields(item), " ")
		if item == "" {
			item = "continue"
		}
		previews = append(previews, truncate.ClipRunes(item, inboxPreviewRunes))
	}
	if hidden := count - limit; hidden > 0 {
		previews = append(previews, fmt.Sprintf("+%d", hidden))
	}
	return fmt.Sprintf("inbox[%d] %s", count, strings.Join(previews, " | "))
}

func (l *LiveStatus) toolLines() []string {
	lines := make([]string, 0, len(l.order))
	for _, id := range l.order {
		if event, ok := l.tools[id]; ok {
			if line := l.renderToolLine(event); line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func (l *LiveStatus) formatTurnDetails() string {
	if l == nil || l.turn <= 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("turn %d", l.turn)}
	if l.turnToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", l.turnToolCalls))
	}
	if l.showUsage {
		contextTokens := l.contextTokens
		if l.turnUsage != nil {
			parts = append(parts, formatTokenUsage(l.turnUsage))
			if l.turnUsage.PromptTokens > 0 {
				contextTokens = l.turnUsage.PromptTokens
			}
		} else if l.outputEstimate > 0 {
			parts = append(parts, outputTokenMarker+"≈"+util.FormatNumber(l.outputEstimate))
		}
		if context := l.ContextUsage(contextTokens); context != "" {
			parts = append(parts, context)
		}
	}
	parts = append(parts, elapsedSentinel)
	return "[" + strings.Join(parts, " | ") + "]"
}

func (l *LiveStatus) ContextUsage(tokens int) string {
	if l == nil {
		return ""
	}
	if tokens <= 0 {
		tokens = l.contextTokens
	}
	if tokens <= 0 || l.contextWindow <= 0 {
		return ""
	}
	return fmt.Sprintf("%s%s/%s (%s)",
		contextMarker,
		util.FormatNumber(tokens),
		util.FormatNumber(l.contextWindow),
		formatUsagePercent(tokens, l.contextWindow))
}

func usageTotal(usage *agent.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}

func formatUsagePercent(used, total int) string {
	if used <= 0 || total <= 0 {
		return "0%"
	}
	pct := float64(used) / float64(total) * 100
	if pct > 0 && pct < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func (l *LiveStatus) clearTools() {
	l.tools = make(map[string]*toolEvent)
	l.order = nil
}

func (l *LiveStatus) ensureTools() {
	if l.tools == nil {
		l.tools = make(map[string]*toolEvent)
	}
}

func (l *LiveStatus) hasTool(id string) bool {
	_, ok := l.tools[id]
	return ok
}

func (l *LiveStatus) allToolsDone() bool {
	if len(l.order) == 0 {
		return false
	}
	for _, id := range l.order {
		event, ok := l.tools[id]
		if !ok || !event.done {
			return false
		}
	}
	return true
}
