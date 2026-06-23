package tui

import (
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

type AgentOutput struct {
	mu        sync.Mutex
	color     output.Color
	debug     bool
	verbosity int

	stream  *StreamWriter
	aborted bool

	// Stats (tool call/error counts tracked here; token usage comes from events).
	turnStart      time.Time
	agentStart     time.Time
	toolCallCount  int
	toolErrorCount int

	// Transient UI.
	mode RenderMode
	tty  bool
	live *LiveStatus
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
	if option != nil {
		debug = option.Debug
		verbosity = len(option.Verbose)
		if option.Quiet {
			verbosity = -1
		}
		noColor = option.NoColor
		model = option.Model
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
	}
	o.live = NewLiveStatus(lv, o.dim, o.renderToolLine)
	o.live.SetContextWindow(agent.ModelContextWindow(model))
	return o
}

func AgentStreamingEnabled(_ *cfg.Option) bool { return true }

// Stderr returns the stream writer's stderr for direct output.
func (o *AgentOutput) Stderr() io.Writer { return o.stream.stderr }

// Stdout returns the stream writer's stdout.
func (o *AgentOutput) Stdout() io.Writer { return o.stream.stdout }

// Markdown returns whether markdown rendering is enabled.
func (o *AgentOutput) Markdown() bool { return o.stream.markdown }

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

func (o *AgentOutput) Queued(text string) {
	if o == nil || o.verbosity < 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopLive()
	o.stream.Flush()
	w := o.Stderr()
	text = truncate.Clip(text, agentStatusPreviewLimit)
	if text == "" {
		fmt.Fprintln(w, o.bold("queued"))
	} else {
		fmt.Fprintf(w, "%s %s\n", o.bold("queued:"), text)
	}
}

func (o *AgentOutput) QueuedFollowUp(text string) { o.Queued("follow-up: " + text) }

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

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

func (o *AgentOutput) HandleEvent(event agent.Event) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.aborted {
		return
	}
	switch event.Type {
	case agent.EventAgentStart:
		o.agentStart = time.Now()

	case agent.EventTurnStart:
		o.stream.NewTurn()
		o.turnStart = time.Now()
		if o.verbosity >= 1 && event.Turn > 1 {
			o.stream.EnsureNewline()
			fmt.Fprintln(o.Stderr(), o.dim("  turn "+fmt.Sprint(event.Turn)))
		}
		if o.canAnimate() {
			o.live.BeginTurn()
		}

	case agent.EventMessageUpdate:
		contentDelta := o.stream.WouldPrintContentDelta(event.Message.Content)
		visible := o.stream.WouldPrintDelta(event.Message.Content, event.Message.ReasoningContent)
		if o.verbosity >= 0 {
			writeDelta := func() {
				o.stream.Delta(event.Message.Content, event.Message.ReasoningContent)
			}
			if o.canAnimate() && !o.live.HasTools() && visible {
				o.live.WithHidden(func() {
					writeDelta()
					o.stream.EnsureLiveBoundary()
				})
			} else {
				writeDelta()
			}
		}
		if o.canAnimate() {
			o.live.MessageUpdate(event, contentDelta)
		}

	case agent.EventToolExecutionStart:
		if o.canAnimate() {
			if !o.live.HasTools() {
				o.live.Stop()
				o.stream.Flush()
			}
			o.live.StartTool(event)
		} else {
			o.live.Stop()
			o.stream.Flush()
			if o.verbosity >= 0 {
				name := toolNameOrDefault(event)
				w := o.Stderr()
				fmt.Fprintln(w)
				fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
					o.color.Wrap("▸", output.ANSICyan)+" "+o.bold(name)+"  "+
						o.dim(truncate.Clip(summarizeToolArguments(name, event.Arguments), 80)))
				if o.verbosity >= 1 {
					o.printToolArgBlock(w, name, event.Arguments)
				}
				if o.debug {
					if args := compactAgentJSON(event.Arguments, agentDebugPreviewLimit); args != "" {
						fmt.Fprintf(w, "%s%s\n", toolArgIndent, o.dim("raw: "+args))
					}
				}
			}
		}

	case agent.EventToolExecutionEnd:
		o.toolCallCount++
		if event.IsError || event.Err != nil {
			o.toolErrorCount++
		}
		if tracked, done := o.live.UpdateTool(event); tracked {
			if done {
				o.printPermanentTools(o.live.StopAndDrainTools())
			}
		} else {
			o.stopLive()
			if o.verbosity >= 0 {
				w := o.Stderr()
				fmt.Fprintln(w)
				fmt.Fprintln(w, o.renderToolLine(event))
				if o.verbosity >= 1 {
					o.printToolDetail(w, event)
				}
			}
		}

	case agent.EventTurnEnd:
		o.live.FinishTurn(event)
		o.stopLive()
		o.turnEnd(event)
	case agent.EventAgentEnd:
		o.live.FinishAgent(event)
		o.stopLive()
		o.agentEnd(event)
	case agent.EventEvalStart:
		o.stopLive()
		o.evalStart(event)
	case agent.EventEvalEnd:
		o.stopLive()
		o.evalEnd(event)
	case agent.EventEvalError:
		o.stopLive()
		o.evalError(event)
	}
}

// ---------------------------------------------------------------------------
// Tool rendering
// ---------------------------------------------------------------------------

func (o *AgentOutput) canAnimate() bool {
	return o != nil && o.mode == ModeInteractive && o.tty && o.verbosity >= 0
}

func (o *AgentOutput) renderToolLine(ev agent.Event) string {
	name := toolNameOrDefault(ev)
	summary := truncate.Clip(summarizeToolArguments(name, ev.Arguments), 80)
	if ev.Type == agent.EventToolExecutionEnd {
		marker, mc := "✓", output.ANSIGreen
		if ev.IsError || ev.Err != nil {
			marker, mc = "✗", output.ANSIRed
		}
		line := o.color.Wrap(marker, mc) + " " + o.bold(name)
		if summary != "" {
			line += "  " + o.dim(summary)
		}
		if len(ev.Result) > 0 {
			line += "  " + o.dim(truncate.FormatSize(len(ev.Result)))
		}
		if elapsed := o.coloredElapsed(ev.StartedAt); elapsed != "" {
			line += "  " + elapsed
		}
		return toolBlockIndent + line
	}
	line := spinnerSentinel + " " + o.bold(name)
	if summary != "" {
		line += "  " + o.dim(summary)
	}
	return toolBlockIndent + line
}

func (o *AgentOutput) printToolDetail(w io.Writer, ev agent.Event) {
	name := toolNameOrDefault(ev)
	if ev.IsError || ev.Err != nil {
		errText := strings.TrimSpace(ev.Result)
		if ev.Err != nil {
			errText = ev.Err.Error()
		}
		if errText != "" {
			fmt.Fprintf(w, "%s%s\n", toolResultIndent,
				o.color.Wrap(truncate.Clip(errText, agentStatusPreviewLimit), output.ANSIRed))
		}
		return
	}
	result := strings.TrimSpace(ev.Result)
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
		if args := decodeToolArguments(ev.Arguments); args != nil {
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

func (o *AgentOutput) printPermanentTools(events []agent.Event) {
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

func (o *AgentOutput) turnEnd(event agent.Event) {
	if o.verbosity < 0 {
		return
	}
	o.stream.Flush()
	w := o.Stderr()

	if o.verbosity >= 2 && o.stream.ReasoningPrinted() == 0 && event.Message.ReasoningContent != nil {
		if reasoning := strings.TrimSpace(*event.Message.ReasoningContent); reasoning != "" {
			o.renderThinkingBlock(w, reasoning)
		}
	}
	if o.stream.ContentPrinted() == 0 && event.Message.Content != nil {
		if content := strings.TrimSpace(*event.Message.Content); content != "" {
			if rendered := renderAgentMarkdown(content, o.Markdown()); rendered != "" {
				fmt.Fprintln(o.Stdout(), rendered)
			}
			o.stream.MarkStreamed()
		}
	}
	o.renderTurnStats(w, event)
	if o.debug {
		role, contentLen, toolCalls, reasoningLen, preview := summarizeChatMessage(event.Message)
		if role != "" || contentLen > 0 || toolCalls > 0 || reasoningLen > 0 {
			fmt.Fprintf(w, "%s[debug] [turn %d] role=%s content=%d reasoning=%d tool_calls=%d preview=%q%s\n",
				o.color.Code(output.ANSIDim), event.Turn, role, contentLen, reasoningLen, toolCalls, preview,
				o.color.Code(output.ANSIReset))
		}
		if event.Usage != nil {
			cache := ""
			if event.Usage.CacheReadTokens > 0 || event.Usage.CacheWriteTokens > 0 {
				cache = fmt.Sprintf(" cache_read=%d cache_write=%d (%.0f%%)",
					event.Usage.CacheReadTokens, event.Usage.CacheWriteTokens,
					event.Usage.CacheHitRatio()*100)
			}
			fmt.Fprintf(w, "%s[debug] [turn %d] prompt=%d completion=%d total=%d context=%d%s%s\n",
				o.color.Code(output.ANSIDim), event.Turn,
				event.Usage.PromptTokens, event.Usage.CompletionTokens, event.Usage.TotalTokens,
				event.ContextTokens, cache, o.color.Code(output.ANSIReset))
		}
	}
}

func (o *AgentOutput) renderTurnStats(w io.Writer, event agent.Event) {
	if w == nil {
		return
	}
	elapsed := time.Since(o.turnStart)
	toolCalls := max(len(event.Message.ToolCalls), len(event.ToolResults))
	parts := []string{fmt.Sprintf("turn %d", event.Turn)}
	if toolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", toolCalls))
	}
	if event.Usage != nil {
		parts = append(parts, formatTokenUsage(event.Usage))
	}
	if context := o.live.ContextUsage(event.ContextTokens); context != "" {
		parts = append(parts, context)
	}
	parts = append(parts, util.FormatDuration(elapsed))
	fmt.Fprintln(w, o.dim("  ["+strings.Join(parts, " | ")+"]"))
	fmt.Fprintln(w)
}

func (o *AgentOutput) agentEnd(event agent.Event) {
	o.stream.EnsureNewline()
	w := o.Stderr()
	if w != nil && event.Turn > 0 {
		elapsed := time.Since(o.agentStart)
		parts := []string{
			fmt.Sprintf("agent %s", event.Stop),
			fmt.Sprintf("turns=%d", event.Turn),
		}
		if o.toolCallCount > 0 {
			toolPart := fmt.Sprintf("tools=%d", o.toolCallCount)
			if o.toolErrorCount > 0 {
				toolPart += fmt.Sprintf(" (%d err)", o.toolErrorCount)
			}
			parts = append(parts, toolPart)
		}
		if event.TotalUsage != nil && event.TotalUsage.TotalTokens > 0 {
			parts = append(parts, formatTokenUsage(event.TotalUsage))
		}
		parts = append(parts, util.FormatDuration(elapsed))
		if event.Err != nil {
			parts = append(parts, fmt.Sprintf("err=%q", event.Err.Error()))
		}
		fmt.Fprintln(w, o.dim("  ["+strings.Join(parts, " | ")+"]"))
	}
	if !o.debug {
		return
	}
	lastRole, lastContentLen, lastToolCalls, lastReasoningLen, lastPreview := lastMessageSummary(event.Messages)
	noToolAssistant := lastRole == "assistant" && lastToolCalls == 0
	hint := ""
	if event.Stop == agent.StopReasonCompleted && noToolAssistant {
		hint = " hint=no_tool_calls_no_pending_work"
	}
	errText := ""
	if event.Err != nil {
		errText = fmt.Sprintf(" err=%q", event.Err.Error())
	}
	fmt.Fprintf(w, "%s[debug] [agent] stop=%s turns=%d messages=%d new=%d last_role=%s content=%d reasoning=%d tools=%d preview=%q%s%s%s\n",
		o.color.Code(output.ANSIDim), event.Stop, event.Turn,
		len(event.Messages), len(event.NewMessages),
		lastRole, lastContentLen, lastReasoningLen, lastToolCalls,
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

func (o *AgentOutput) evalStart(event agent.Event) {
	w := o.Stderr()
	if w == nil {
		return
	}
	if o.canAnimate() {
		o.live.ShowEvalRound(event.EvalRound)
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
			o.color.Wrap("⋯", output.ANSICyan)+" "+o.bold("eval")+"  "+o.dim(fmt.Sprintf("round %d", event.EvalRound+1)))
	}
}

func (o *AgentOutput) evalEnd(event agent.Event) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	marker, mc, status := "✓", output.ANSIGreen, "pass"
	if !event.EvalPass {
		marker, mc, status = "⟳", output.ANSIYellow, "fail"
	}
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap(marker, mc)+" "+o.bold("eval")+"  "+
			o.dim(fmt.Sprintf("round %d", event.EvalRound+1))+"  "+o.dim(status))
	if reason := strings.TrimSpace(event.EvalReason); reason != "" {
		fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.dim(reason))
	}
}

func (o *AgentOutput) evalError(event agent.Event) {
	w := o.Stderr()
	if w == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%s\n", toolBlockIndent,
		o.color.Wrap("⚠", output.ANSIYellow)+" "+o.bold("eval")+"  "+
			o.dim(fmt.Sprintf("round %d", event.EvalRound+1))+"  "+o.dim("error"))
	detail := "evaluator LLM call failed"
	if event.EvalError != "" {
		detail = event.EvalError
	}
	fmt.Fprintf(w, "%s%s\n", toolResultIndent, o.dim(detail+", continuing..."))
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
