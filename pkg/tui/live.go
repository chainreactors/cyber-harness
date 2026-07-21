package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/util"
)

const (
	liveStatusWidth    = len(liveStatusThinking)
	liveStatusThinking = "thinking"
	liveStatusTooling  = "tooling"
	liveStatusTalking  = "talking"
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

	turnUsage      *agent.Usage
	completedUsage agent.Usage
	contextTokens  int
	contextWindow  int

	tools map[string]*toolEvent
	order []string

	dim            func(string) string
	renderToolLine func(*toolEvent) string
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
	l.turnUsage = nil
	l.completedUsage = agent.Usage{}
	l.contextTokens = 0
	l.tools = make(map[string]*toolEvent)
	l.order = nil
}

func (l *LiveStatus) BeginTurn() {
	if l == nil {
		return
	}
	l.status = liveStatusThinking
	l.note = ""
	l.turnUsage = nil
	l.clearTools()
	l.Render()
}

// NoteDelta reflects streaming progress: text output flips the status to
// talking unless tools are running.
func (l *LiveStatus) NoteDelta(textDelta bool) {
	if l == nil {
		return
	}
	if textDelta && !l.HasTools() {
		l.status = liveStatusTalking
		l.note = ""
	}
	l.Render()
}

func (l *LiveStatus) SetTurnUsage(usage agent.Usage) {
	if l == nil {
		return
	}
	l.turnUsage = &usage
}

func (l *LiveStatus) ShowEvalRound(round int) {
	if l == nil {
		return
	}
	l.status = liveStatusTooling
	l.note = fmt.Sprintf("eval · round %d", round+1)
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

// FinishTurn folds the turn's usage into the completed totals and records the
// latest context size.
func (l *LiveStatus) FinishTurn(contextTokens int) {
	if l == nil {
		return
	}
	if l.turnUsage != nil {
		l.addCompleted(l.turnUsage)
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
	if usage := l.formatTokenDetails(); usage != "" {
		details = append(details, l.dim(usage))
	}
	if l.note != "" {
		details = append(details, l.dim(l.note))
	}
	if len(details) > 0 {
		line += " · " + strings.Join(details, " · ")
	}
	return line
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

func (l *LiveStatus) addCompleted(usage *agent.Usage) {
	if usage == nil {
		return
	}
	l.completedUsage.PromptTokens += usage.PromptTokens
	l.completedUsage.CompletionTokens += usage.CompletionTokens
	l.completedUsage.TotalTokens += usageTotal(usage)
	l.completedUsage.CacheReadTokens += usage.CacheReadTokens
	l.completedUsage.CacheWriteTokens += usage.CacheWriteTokens
}

func (l *LiveStatus) formatTokenDetails() string {
	total := usageTotal(&l.completedUsage)
	output := 0
	contextTokens := l.contextTokens
	if l.turnUsage != nil {
		total += usageTotal(l.turnUsage)
		output = l.turnUsage.CompletionTokens
		if l.turnUsage.PromptTokens > 0 {
			contextTokens = l.turnUsage.PromptTokens
		}
	}
	if total == 0 && output == 0 && contextTokens == 0 {
		return ""
	}

	parts := make([]string, 0, 3)
	if total > 0 {
		parts = append(parts, "tokens="+util.FormatNumber(total))
	}
	if context := l.ContextUsage(contextTokens); context != "" {
		parts = append(parts, context)
	}
	if output > 0 {
		parts = append(parts, "out="+util.FormatNumber(output))
	}
	return strings.Join(parts, " ")
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
	return fmt.Sprintf("ctx=%s/%s (%s)",
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
