package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/aop"
	xcompact "github.com/chainreactors/aiscan/pkg/aop/x/compact"
	xeval "github.com/chainreactors/aiscan/pkg/aop/x/eval"
	"github.com/chainreactors/aiscan/pkg/util"
	"golang.org/x/term"
)

const (
	agentStatusPreviewLimit  = 180
	agentDebugPreviewLimit   = 320
	toolResultPreviewDefault = 8
	toolResultPreviewWidth   = 140
	toolFetchBodyLines       = 4
	toolBlockIndent          = "  "
	toolArgIndent            = "    "
	toolResultIndent         = "      "
	thinkingPreviewMaxLines  = 20
)

// ---------------------------------------------------------------------------
// AgentOutput
// ---------------------------------------------------------------------------

// deltaAccumulator joins a message's AOP message.delta fragments into the
// cumulative content/reasoning strings StreamWriter.Delta expects.
type deltaAccumulator struct {
	text      string
	reasoning string
}

type AgentOutput struct {
	mu        sync.Mutex
	color     output.Color
	debug     bool
	verbosity int

	stream  *StreamWriter
	aborted bool

	// Stats (tool call/error counts tracked here; token usage comes from events).
	agentStart     time.Time
	toolCallCount  int
	toolErrorCount int

	// AOP stream state: per-message delta accumulators (cumulative strings fed
	// to StreamWriter), the current turn's last complete assistant message,
	// and usage totals for the turn-end / session-end stat lines.
	deltas        map[string]*deltaAccumulator
	lastAssistant aop.MessageData
	hasAssistant  bool
	turnUsage     *agent.Usage
	totalUsage    agent.Usage
	turnToolCalls int
	contextTokens int
	runCount      int

	// Transient UI.
	mode                   RenderMode
	tty                    bool
	interactiveInputActive bool
	live                   *LiveStatus
	readline               bool
}

func NewAgentOutput(option *cfg.Option) *AgentOutput {
	return newAgentOutput(option, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stdout.Fd())),
		term.IsTerminal(int(os.Stderr.Fd())),
		resolveRenderMode())
}

func NewStaticAgentOutput(option *cfg.Option) *AgentOutput {
	return newAgentOutput(option, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stdout.Fd())),
		term.IsTerminal(int(os.Stderr.Fd())),
		ModeStatic)
}

func NewAgentOutputWithWriters(option *cfg.Option, stdout, stderr io.Writer, terminal bool) *AgentOutput {
	return newAgentOutputWithWriters(option, stdout, stderr, terminal, resolveRenderMode())
}

func NewStaticAgentOutputWithWriters(option *cfg.Option, stdout, stderr io.Writer, terminal bool) *AgentOutput {
	return newAgentOutputWithWriters(option, stdout, stderr, terminal, ModeStatic)
}

func newAgentOutputWithWriters(option *cfg.Option, stdout, stderr io.Writer, terminal bool, mode RenderMode) *AgentOutput {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = stdout
	}
	return newAgentOutput(option, stdout, stderr, terminal, terminal, mode)
}

func newAgentOutput(option *cfg.Option, stdout, stderr io.Writer, stdoutTTY, stderrTTY bool, mode RenderMode) *AgentOutput {
	debug := false
	verbosity := 0
	noColor := false
	model := ""
	contextWindow := 0
	if option != nil {
		debug = option.Debug
		verbosity = len(option.Verbose)
		if option.Quiet {
			verbosity = -1
		}
		noColor = option.NoColor
		model = option.Model
		contextWindow = option.ContextWindow
	}
	useColor := !noColor && stderrTTY
	color := output.NewColor(useColor)
	lv := NewLiveView(stderr, color.Code(output.ANSICyan))
	o := &AgentOutput{
		color:     color,
		debug:     debug,
		verbosity: verbosity,
		stream:    NewStreamWriter(stdout, stderr, stdoutTTY, !noColor && stdoutTTY, color, verbosity),
		mode:      mode,
		tty:       stderrTTY,
		deltas:    make(map[string]*deltaAccumulator),
	}
	o.live = NewLiveStatus(lv, o.dim, o.renderToolLine)
	if contextWindow <= 0 {
		contextWindow = agent.ModelContextWindow(model)
	}
	o.live.SetContextWindow(contextWindow)
	return o
}

// Stderr returns the stream writer's stderr for direct output.
func (o *AgentOutput) Stderr() io.Writer { return o.stream.stderr }

// Stdout returns the stream writer's stdout.
func (o *AgentOutput) Stdout() io.Writer { return o.stream.stdout }

// Markdown returns whether markdown rendering is enabled.
func (o *AgentOutput) Markdown() bool { return o.stream.markdown }

func (o *AgentOutput) SetContextWindow(tokens int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.live.SetContextWindow(tokens)
}

// SetReadlineMode commits finalized output above the current prompt and sends
// transient status frames through readline's composer. The terminal still owns
// scrollback; the 100ms animation only redraws the active composer.
func (o *AgentOutput) SetReadlineMode(output io.Writer, status func(string)) {
	if o == nil || output == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.readline = true
	o.stream.stdout = output
	o.stream.stderr = output
	o.live.view.SetStatusSink(status)
}

// ---------------------------------------------------------------------------
// Verbosity
// ---------------------------------------------------------------------------

func (o *AgentOutput) SetVerbosity(level int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.verbosity = level
	o.stream.verbosity = level
}

func (o *AgentOutput) VerbosityLevel() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.verbosity
}

func (o *AgentOutput) VerbosityLabel() string {
	switch o.VerbosityLevel() {
	case -1:
		return "quiet"
	case 0:
		return "default"
	case 1:
		return "tools"
	default:
		return "thinking"
	}
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func (o *AgentOutput) Start(label, text string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopLive()
	o.stream.Flush()
	o.beginRun()
	if o.verbosity < 0 {
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "task"
	}
	if label == "prompt" {
		if body := strings.TrimRight(text, "\n"); shouldRenderUserIntent(body) {
			o.renderUserIntent(body)
		}
		return
	}
	w := o.Stderr()
	text = truncate.Clip(text, agentStatusPreviewLimit)
	if text == "" {
		fmt.Fprintf(w, "%s\n", o.bold("> "+label))
	} else {
		fmt.Fprintf(w, "%s %s\n", o.bold("> "+label+":"), text)
	}
}

func (o *AgentOutput) Empty() {
	if o == nil || o.verbosity < 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.aborted {
		o.stopLive()
		o.stream.Flush()
		fmt.Fprintln(o.Stderr(), o.dim("No output."))
	}
}

func (o *AgentOutput) Final(content string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.aborted {
		return
	}
	o.stopLive()
	if o.stream.Streamed() {
		o.stream.Flush()
		o.stream.Reset()
		return
	}
	if rendered := renderAgentMarkdown(content, o.Markdown()); rendered != "" {
		fmt.Fprintln(o.Stdout(), rendered)
	}
}

// SetInbox updates the transient preview of prompts waiting behind the active
// run without stopping the live status.
func (o *AgentOutput) SetInbox(items []string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	running := o.live.Running()
	render := o.canAnimate() && (running || len(items) > 0)
	o.live.SetInbox(items, render)
}

func (o *AgentOutput) Stopping() {
	if o == nil || o.verbosity < 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopLive()
	o.stream.Flush()
}

func (o *AgentOutput) Stopped() {
	if o == nil || o.verbosity < 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopLive()
	o.stream.Flush()
	fmt.Fprintln(o.Stderr(), o.dim("Task stopped."))
}

func (o *AgentOutput) Error(err error) {
	if o == nil || err == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.aborted {
		o.stopLive()
		o.stream.Flush()
		fmt.Fprintf(o.Stderr(), "error: %s\n", err)
	}
}

func (o *AgentOutput) AbortCurrentRun() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.live.Reset()
	o.stream.Flush()
	o.stream.Reset()
	o.aborted = true
}

func (o *AgentOutput) EnsureStreamNewline() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stream.EnsureNewline()
}

func (o *AgentOutput) SetInteractiveInputActive(active bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.interactiveInputActive = active
	if active && !o.readline {
		o.stopLive()
	}
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

func (o *AgentOutput) HandleEvent(event aop.Event) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.aborted {
		return
	}
	switch event.Type {
	case aop.TypeSessionStart:
		o.agentStart = time.Now()

	case aop.TypeTurnStart:
		o.agentStart = time.Now()
		o.runCount++
		o.stream.NewTurn()
		o.turnUsage = nil
		o.totalUsage = agent.Usage{}
		o.turnToolCalls = 0
		o.lastAssistant = aop.MessageData{}
		o.hasAssistant = false
		if o.canAnimate() {
			o.live.BeginTurn(o.runCount)
		}

	case aop.TypeMessageDelta:
		data, err := aop.DecodeData[aop.MessageDeltaData](event)
		if err != nil || data.MessageID == "" {
			return
		}
		acc := o.deltas[data.MessageID]
		if acc == nil {
			acc = &deltaAccumulator{}
			o.deltas[data.MessageID] = acc
		}
		if data.PartType == aop.PartReasoning {
			acc.reasoning += data.Delta
		} else {
			acc.text += data.Delta
		}
		o.live.SetOutputEstimate(estimateStreamTokens(acc.text, acc.reasoning))
		contentDelta := o.stream.WouldPrintContentDelta(&acc.text)
		visible := o.stream.WouldPrintDelta(&acc.text, &acc.reasoning)
		if o.verbosity >= 0 {
			writeDelta := func() {
				o.stream.Delta(&acc.text, &acc.reasoning)
			}
			if o.canAnimate() && !o.live.HasTools() && visible {
				o.live.WithHidden(func() {
					writeDelta()
					// The readline bridge already commits complete lines above the
					// prompt. Forcing a boundary here turned every reasoning token
					// delta into a separate line under -vv.
					if !o.readline {
						o.stream.EnsureLiveBoundary()
					}
				})
			} else {
				writeDelta()
			}
		}
		if o.canAnimate() {
			o.live.NoteDelta(contentDelta)
		}

	case aop.TypeMessage:
		data, err := aop.DecodeData[aop.MessageData](event)
		if err != nil {
			return
		}
		delete(o.deltas, data.MessageID)
		if data.Role == "assistant" {
			o.lastAssistant = data
			o.hasAssistant = true
			if event.TurnID == "" {
				if content := strings.TrimSpace(messagePartText(data, aop.PartText)); content != "" {
					if rendered := renderAgentMarkdown(content, o.Markdown()); rendered != "" {
						fmt.Fprintln(o.Stdout(), rendered)
					}
				}
			}
		}

	case aop.TypeToolCall:
		data, err := aop.DecodeData[aop.ToolCallData](event)
		if err != nil {
			return
		}
		o.turnToolCalls++
		o.live.SetTurnToolCalls(o.turnToolCalls)
		ev := &toolEvent{
			id:        data.ToolCallID,
			name:      data.ToolName,
			args:      marshalToolArgs(data.Args),
			startedAt: time.Now(),
		}
		if o.canAnimate() {
			if !o.live.HasTools() {
				o.live.Stop()
				o.stream.Flush()
			}
			o.live.StartTool(ev)
		} else {
			o.live.Stop()
			o.stream.Flush()
			if o.verbosity >= 0 {
				name := toolNameOrDefault(ev)
				w := o.Stderr()
				fmt.Fprintln(w)
				fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
					o.color.Wrap("▸", output.ANSICyan)+" "+o.bold(name)+"  "+
						o.dim(truncate.Clip(summarizeToolArguments(name, ev.args), 80)))
				if o.verbosity >= 1 {
					o.printToolArgBlock(w, name, ev.args)
				}
				if o.debug {
					if args := compactAgentJSON(ev.args, agentDebugPreviewLimit); args != "" {
						fmt.Fprintf(w, "%s%s\n", toolArgIndent, o.dim("raw: "+args))
					}
				}
			}
		}

	case aop.TypeToolResult:
		data, err := aop.DecodeData[aop.ToolResultData](event)
		if err != nil {
			return
		}
		o.toolCallCount++
		if data.IsError {
			o.toolErrorCount++
		}
		ev := &toolEvent{
			id:      data.ToolCallID,
			name:    data.ToolName,
			result:  flattenToolResult(data.Content),
			isError: data.IsError,
			done:    true,
			elapsed: time.Duration(data.DurationMs) * time.Millisecond,
		}
		if tracked, done := o.live.UpdateTool(ev); tracked {
			if done {
				o.printPermanentTools(o.live.StopAndDrainTools())
			}
		} else {
			o.stopLive()
			if o.verbosity >= 0 {
				w := o.Stderr()
				fmt.Fprintln(w)
				fmt.Fprintln(w, o.renderToolLine(ev))
				if o.verbosity >= 1 {
					o.printToolDetail(w, ev)
				}
			}
		}

	case aop.TypeUsage:
		data, err := aop.DecodeData[aop.UsageData](event)
		if err != nil {
			return
		}
		usage := agent.Usage{
			PromptTokens:     data.InputTokens,
			CompletionTokens: data.OutputTokens,
			TotalTokens:      data.TotalTokens,
			CacheReadTokens:  data.CacheReadTokens,
			CacheWriteTokens: data.CacheWriteTokens,
		}
		o.turnUsage = &usage
		o.totalUsage.PromptTokens += usage.PromptTokens
		o.totalUsage.CompletionTokens += usage.CompletionTokens
		o.totalUsage.TotalTokens += usage.TotalTokens
		o.totalUsage.CacheReadTokens += usage.CacheReadTokens
		o.totalUsage.CacheWriteTokens += usage.CacheWriteTokens
		o.live.SetTurnUsage(usage)
		if o.canAnimate() {
			o.live.Render()
		}

	case aop.TypeTurnEnd:
		data, err := aop.DecodeData[aop.TurnEndData](event)
		if err != nil {
			return
		}
		o.contextTokens = data.ContextTokens
		o.live.FinishTurn(o.contextTokens)
		o.stopLive()
		o.turnEnd(o.runCount)
		o.agentEnd(data)
	case aop.TypeSessionEnd:
		o.stopLive()
	case aop.TypeStatus:
		data, err := aop.DecodeData[aop.StatusData](event)
		if err != nil {
			return
		}
		switch data.State {
		case xeval.StateStart:
			detail, _, _ := xeval.GetDetail(event)
			o.stopLive()
			o.evalStart(detail.Round)
		case xeval.StateEnd:
			detail, _, _ := xeval.GetDetail(event)
			o.stopLive()
			o.evalEnd(detail.Round, detail.Pass, detail.Reason)
		case xeval.StateError:
			detail, _, _ := xeval.GetDetail(event)
			o.stopLive()
			o.evalError(detail.Round, detail.Error)
		case xcompact.StateStart:
			o.stopLive()
			o.compactStart()
		case xcompact.StateEnd:
			detail, _, _ := xcompact.GetDetail(event)
			o.stopLive()
			o.compactEnd(detail.TokensBefore, detail.TokensAfter, detail.KeptMessages)
		case xcompact.StateError:
			o.stopLive()
			o.compactError()
		}
	}
}

func estimateStreamTokens(parts ...string) int {
	chars := 0
	for _, part := range parts {
		chars += len(part)
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

// ---------------------------------------------------------------------------
// Tool rendering
// ---------------------------------------------------------------------------

func (o *AgentOutput) canAnimate() bool {
	if o == nil || o.mode != ModeInteractive || !o.tty || o.verbosity < 0 {
		return false
	}
	if o.readline {
		return true
	}
	return !o.interactiveInputActive
}

func (o *AgentOutput) renderToolLine(ev *toolEvent) string {
	name := toolNameOrDefault(ev)
	summary := truncate.Clip(summarizeToolArguments(name, ev.args), 80)
	if ev.done {
		marker, mc := "✓", output.ANSIGreen
		if ev.isError {
			marker, mc = "✗", output.ANSIRed
		}
		line := o.color.Wrap(marker, mc) + " " + o.bold(name)
		if summary != "" {
			line += "  " + o.dim(summary)
		}
		if len(ev.result) > 0 {
			line += "  " + o.dim(truncate.FormatSize(len(ev.result)))
		}
		if elapsed := o.coloredElapsed(ev.startedAt); elapsed != "" {
			line += "  " + elapsed
		}
		return toolBlockIndent + line
	}
	line := spinnerSentinel + " " + o.bold(name)
	if summary != "" {
		line += "  " + o.dim(summary)
	}
	if elapsed := o.coloredElapsed(ev.startedAt); elapsed != "" {
		line += "  " + elapsed
	}
	return toolBlockIndent + line
}

func (o *AgentOutput) printToolDetail(w io.Writer, ev *toolEvent) {
	name := toolNameOrDefault(ev)
	if ev.isError {
		if errText := strings.TrimSpace(ev.result); errText != "" {
			fmt.Fprintf(w, "%s%s\n", toolResultIndent,
				o.color.Wrap(truncate.Clip(errText, agentStatusPreviewLimit), output.ANSIRed))
		}
		return
	}
	result := strings.TrimSpace(ev.result)
	if result == "" {
		return
	}
	var preview toolResultPreview
	if o.verbosity >= 2 {
		preview = toolResultPreview{lines: normalizeToolResultLines(result)}
	} else {
		preview = buildToolResultPreview(name, result, o.debug)
	}
	if len(preview.lines) == 0 {
		return
	}
	if name == "read" && o.color.Enabled {
		if args := decodeToolArguments(ev.args); args != nil {
			if path := stringArg(args, "path"); path != "" {
				preview.lines = highlightReadResult(path, preview.lines, o.color)
			}
		}
	}
	for _, line := range preview.lines {
		if isToolMetaLine(line) {
			fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.color.Wrap(line, output.ANSIYellow))
		} else {
			fmt.Fprintf(w, "%s%s\n", toolResultIndent, line)
		}
	}
	if preview.truncated {
		fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.dim(fmt.Sprintf("… +%d lines hidden", preview.hidden)))
	}
}

func (o *AgentOutput) printToolArgBlock(w io.Writer, name, arguments string) {
	lines := formatToolArguments(name, arguments)
	if len(lines) == 0 {
		return
	}
	maxKey := 0
	for _, l := range lines {
		if len(l.key) > maxKey {
			maxKey = len(l.key)
		}
	}
	for _, l := range lines {
		fmt.Fprintf(w, "%s%s%s%s\n", toolArgIndent,
			o.dim(l.key), strings.Repeat(" ", maxKey-len(l.key)+2), l.value)
	}
}

func (o *AgentOutput) printPermanentTools(events []*toolEvent) {
	if len(events) == 0 {
		return
	}
	w := o.Stderr()
	fmt.Fprintln(w)
	for _, event := range events {
		fmt.Fprintln(w, o.renderToolLine(event))
		if o.verbosity >= 1 {
			o.printToolDetail(w, event)
		}
	}
}

func (o *AgentOutput) stopLive() {
	o.printPermanentTools(o.live.StopAndDrainTools())
}

// ---------------------------------------------------------------------------
// Internal state
// ---------------------------------------------------------------------------

func (o *AgentOutput) beginRun() {
	o.stream.Reset()
	o.aborted = false
	o.live.Reset()
	o.toolCallCount = 0
	o.toolErrorCount = 0
	o.deltas = make(map[string]*deltaAccumulator)
	o.lastAssistant = aop.MessageData{}
	o.hasAssistant = false
	o.turnUsage = nil
	o.totalUsage = agent.Usage{}
	o.turnToolCalls = 0
	o.contextTokens = 0
}

func (o *AgentOutput) dim(text string) string  { return o.color.Wrap(text, output.ANSIDim) }
func (o *AgentOutput) bold(text string) string { return o.color.Wrap(text, output.ANSIBold) }

func (o *AgentOutput) coloredElapsed(started time.Time) string {
	if started.IsZero() {
		return ""
	}
	d := time.Since(started)
	text := "· " + util.FormatDuration(d)
	switch {
	case d > 30*time.Second:
		return o.color.Wrap(text, output.ANSIRed)
	case d > 5*time.Second:
		return o.color.Wrap(text, output.ANSIYellow)
	default:
		return text
	}
}

// ---------------------------------------------------------------------------
// Turn / agent end — stats come from events, not accumulated
// ---------------------------------------------------------------------------

func (o *AgentOutput) turnEnd(turn int) {
	if o.verbosity < 0 {
		return
	}
	o.stream.Flush()
	w := o.Stderr()

	if o.verbosity >= 2 && o.stream.ReasoningPrinted() == 0 {
		if reasoning := strings.TrimSpace(messagePartText(o.lastAssistant, aop.PartReasoning)); reasoning != "" {
			o.renderThinkingBlock(w, reasoning)
		}
	}
	if o.stream.ContentPrinted() == 0 {
		if content := strings.TrimSpace(messagePartText(o.lastAssistant, aop.PartText)); content != "" {
			if rendered := renderAgentMarkdown(content, o.Markdown()); rendered != "" {
				fmt.Fprintln(o.Stdout(), rendered)
			}
			o.stream.MarkStreamed()
		}
	}
	if o.debug {
		role, contentLen, reasoningLen, preview := summarizeMessageData(o.lastAssistant)
		if role != "" || contentLen > 0 || reasoningLen > 0 {
			fmt.Fprintf(w, "%s[debug] [turn %d] role=%s content=%d reasoning=%d tool_calls=%d preview=%q%s\n",
				o.color.Code(output.ANSIDim), turn, role, contentLen, reasoningLen, o.turnToolCalls, preview,
				o.color.Code(output.ANSIReset))
		}
		if o.turnUsage != nil {
			cache := ""
			if o.turnUsage.CacheReadTokens > 0 || o.turnUsage.CacheWriteTokens > 0 {
				cache = fmt.Sprintf(" cache_read=%d cache_write=%d (%.0f%%)",
					o.turnUsage.CacheReadTokens, o.turnUsage.CacheWriteTokens,
					o.turnUsage.CacheHitRatio()*100)
			}
			fmt.Fprintf(w, "%s[debug] [turn %d] prompt=%d completion=%d total=%d context=%d%s%s\n",
				o.color.Code(output.ANSIDim), turn,
				o.turnUsage.PromptTokens, o.turnUsage.CompletionTokens, o.turnUsage.TotalTokens,
				o.contextTokens, cache, o.color.Code(output.ANSIReset))
		}
	}
}

func (o *AgentOutput) agentEnd(data aop.TurnEndData) {
	o.stream.EnsureNewline()
	w := o.Stderr()
	if w != nil && o.debug {
		elapsed := time.Since(o.agentStart)
		parts := []string{
			fmt.Sprintf("agent %s", data.Stop),
		}
		if o.toolCallCount > 0 {
			toolPart := fmt.Sprintf("tools=%d", o.toolCallCount)
			if o.toolErrorCount > 0 {
				toolPart += fmt.Sprintf(" (%d err)", o.toolErrorCount)
			}
			parts = append(parts, toolPart)
		}
		if usageTotal(&o.totalUsage) > 0 {
			parts = append(parts, formatTokenUsage(&o.totalUsage))
		}
		parts = append(parts, util.FormatDuration(elapsed))
		if data.Error != "" {
			parts = append(parts, fmt.Sprintf("err=%q", data.Error))
		}
		fmt.Fprintln(w, o.dim("  ["+strings.Join(parts, " | ")+"]"))
	}
	if !o.debug {
		return
	}
	lastRole, lastContentLen, lastReasoningLen, lastPreview := summarizeMessageData(o.lastAssistant)
	hint := ""
	if data.Stop == string(agent.StopReasonCompleted) && lastRole == "assistant" {
		hint = " hint=no_tool_calls_no_pending_work"
	}
	errText := ""
	if data.Error != "" {
		errText = fmt.Sprintf(" err=%q", data.Error)
	}
	fmt.Fprintf(w, "%s[debug] [agent] stop=%s last_role=%s content=%d reasoning=%d tools=%d preview=%q%s%s%s\n",
		o.color.Code(output.ANSIDim), data.Stop,
		lastRole, lastContentLen, lastReasoningLen, o.turnToolCalls,
		lastPreview, hint, errText, o.color.Code(output.ANSIReset))
}

// ---------------------------------------------------------------------------
// Eval / thinking / user intent
// ---------------------------------------------------------------------------

func (o *AgentOutput) renderThinkingBlock(w io.Writer, reasoning string) {
	for _, line := range o.thinkingBlockLines(reasoning) {
		fmt.Fprintln(w, line)
	}
}

func (o *AgentOutput) thinkingBlockLines(reasoning string) []string {
	reasoning = strings.ReplaceAll(reasoning, "\r\n", "\n")
	reasoning = strings.ReplaceAll(reasoning, "\r", "\n")
	raw := strings.Split(reasoning, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, truncate.ClipRunes(line, agentStatusPreviewLimit))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	if hidden := len(lines) - thinkingPreviewMaxLines; hidden > 0 {
		lines = append([]string{fmt.Sprintf("… +%d earlier lines hidden", hidden)}, lines[hidden:]...)
	}
	for i := range lines {
		lines[i] = o.dim(lines[i])
	}
	return lines
}

func (o *AgentOutput) evalStart(round int) {
	w := o.Stderr()
	if w == nil {
		return
	}
	if o.canAnimate() {
		o.live.ShowEvalRound(round)
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
			o.color.Wrap("⋯", output.ANSICyan)+" "+o.bold("eval")+"  "+o.dim(fmt.Sprintf("round %d", round)))
	}
}

func (o *AgentOutput) evalEnd(round int, pass bool, reason string) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	marker, mc, status := "✓", output.ANSIGreen, "pass"
	if !pass {
		marker, mc, status = "⟳", output.ANSIYellow, "fail"
	}
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap(marker, mc)+" "+o.bold("eval")+"  "+
			o.dim(fmt.Sprintf("round %d", round))+"  "+o.dim(status))
	if reason := strings.TrimSpace(reason); reason != "" {
		fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.dim(reason))
	}
}

func (o *AgentOutput) evalError(round int, evalErr string) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap("⚠", output.ANSIYellow)+" "+o.bold("eval")+"  "+
			o.dim(fmt.Sprintf("round %d", round))+"  "+o.dim("error"))
	detail := "evaluator LLM call failed"
	if evalErr != "" {
		detail = evalErr
	}
	fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.dim(detail+", continuing..."))
}

func (o *AgentOutput) compactStart() {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap("⋯", output.ANSICyan)+" "+o.bold("compact")+"  "+o.dim("compacting context..."))
}

func (o *AgentOutput) compactEnd(tokensBefore, tokensAfter, keptMessages int) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap("✓", output.ANSIGreen)+" "+o.bold("compact")+"  "+
			o.dim(fmt.Sprintf("~%d → ~%d tokens (%d messages kept)",
				tokensBefore, tokensAfter, keptMessages)))
}

func (o *AgentOutput) compactError() {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap("⚠", output.ANSIYellow)+" "+o.bold("compact")+"  "+o.dim("failed"))
}

func (o *AgentOutput) renderUserIntent(body string) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w, o.dim("╭─ ")+o.bold("user"))
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(w, o.dim("│"))
	} else {
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(w, "%s %s\n", o.dim("│"), line)
		}
	}
	fmt.Fprintln(w, o.dim("╰─"))
}

// marshalToolArgs normalizes a tool.call Args payload (raw JSON string or a
// decoded value) into the JSON string the argument summarizers expect.
func marshalToolArgs(args any) string {
	switch v := args.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

// flattenToolResult reduces a tool.result Content variant (plain string or
// ToolResultContent) to its display text; images are not rendered in the TUI.
func flattenToolResult(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case aop.ToolResultContent:
		return v.Content
	case *aop.ToolResultContent:
		return v.Content
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

// messagePartText joins the text of all parts of one type in a message.
func messagePartText(msg aop.MessageData, partType string) string {
	var sb strings.Builder
	for _, p := range msg.Parts {
		if p.Type != partType || p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}
