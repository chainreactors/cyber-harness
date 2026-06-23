package tui

import (
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
)

const (
	liveStatusWidth    = len(liveStatusThinking)
	liveStatusThinking = "thinking"
	liveStatusTooling  = "tooling"
	liveStatusTalking  = "talking"
)

type LiveStatus struct {
	view *LiveView

	status string
	note   string
	usage  *agent.Usage

	tools map[string]agent.Event
	order []string

	dim            func(string) string
	renderToolLine func(agent.Event) string
}

func NewLiveStatus(view *LiveView, dim func(string) string, renderToolLine func(agent.Event) string) *LiveStatus {
	if dim == nil {
		dim = func(s string) string { return s }
	}
	if renderToolLine == nil {
		renderToolLine = func(agent.Event) string { return "" }
	}
	return &LiveStatus{
		view:           view,
		status:         liveStatusThinking,
		tools:          make(map[string]agent.Event),
		dim:            dim,
		renderToolLine: renderToolLine,
	}
}

func (l *LiveStatus) Reset() {
	if l == nil {
		return
	}
	l.Stop()
	l.status = liveStatusThinking
	l.note = ""
	l.usage = nil
	l.tools = make(map[string]agent.Event)
	l.order = nil
}

func (l *LiveStatus) BeginTurn() {
	if l == nil {
		return
	}
	l.status = liveStatusThinking
	l.note = ""
	l.usage = nil
	l.clearTools()
	l.Render()
}

func (l *LiveStatus) SetUsage(usage *agent.Usage) {
	if l == nil || usage == nil {
		return
	}
	copied := *usage
	l.usage = &copied
}

func (l *LiveStatus) SetTalking() {
	if l == nil {
		return
	}
	l.status = liveStatusTalking
	l.note = ""
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

func (l *LiveStatus) StartTool(event agent.Event) {
	if l == nil {
		return
	}
	l.status = liveStatusTooling
	l.note = ""
	if event.ToolCallID != "" {
		l.ensureTools()
		if !l.hasTool(event.ToolCallID) {
			l.order = append(l.order, event.ToolCallID)
		}
		l.tools[event.ToolCallID] = event
	}
	l.Render()
}

func (l *LiveStatus) UpdateTool(event agent.Event) (tracked bool, done bool) {
	if l == nil || event.ToolCallID == "" || !l.hasTool(event.ToolCallID) {
		return false, false
	}
	l.status = liveStatusTooling
	l.note = ""
	l.ensureTools()
	l.tools[event.ToolCallID] = event
	if l.allToolsDone() {
		return true, true
	}
	l.Render()
	return true, false
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

func (l *LiveStatus) StopAndDrainTools() []agent.Event {
	if l == nil {
		return nil
	}
	l.Stop()
	return l.DrainTools()
}

func (l *LiveStatus) DrainTools() []agent.Event {
	if l == nil || len(l.order) == 0 {
		return nil
	}
	events := make([]agent.Event, 0, len(l.order))
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
	if usage := formatLiveTokenUsage(l.usage); usage != "" {
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

func (l *LiveStatus) clearTools() {
	l.tools = make(map[string]agent.Event)
	l.order = nil
}

func (l *LiveStatus) ensureTools() {
	if l.tools == nil {
		l.tools = make(map[string]agent.Event)
	}
}

func (l *LiveStatus) hasTool(id string) bool {
	for _, existing := range l.order {
		if existing == id {
			return true
		}
	}
	return false
}

func (l *LiveStatus) allToolsDone() bool {
	if len(l.order) == 0 {
		return false
	}
	for _, id := range l.order {
		event, ok := l.tools[id]
		if !ok || event.Type != agent.EventToolExecutionEnd {
			return false
		}
	}
	return true
}
