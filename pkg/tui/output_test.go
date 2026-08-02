package tui

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	ext "github.com/chainreactors/aiscan/pkg/types/extensions"
)

type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// ---------------------------------------------------------------------------
// AOP event builders
// ---------------------------------------------------------------------------

func turnStartEvent(turn int) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}}
}

func turnEndEvent(turn, contextTokens int) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{
		StopReason: string(agent.StopReasonCompleted), ContextTokens: uint64(contextTokens),
	}}}
}

func textDeltaEvent(messageID, delta string) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
		MessageId: messageID, Value: &aop.MessageDelta_Text{Text: delta},
	}}}
}

func reasoningDeltaEvent(messageID, delta string) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{
		MessageId: messageID, Value: &aop.MessageDelta_Reasoning{Reasoning: delta},
	}}}
}

func messageEvent(messageID, role string, content ...*aop.Content) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_Message{Message: &aop.Message{
		Id: messageID, Role: role, Content: content,
	}}}
}

func toolCallEvent(id, name, args string) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{
		Id: id, Name: name, Arguments: &aop.EncodedValue{Data: []byte(args), MediaType: aop.JSONMediaType},
	}}}
}

func toolResultEvent(id, name, result string, isError bool) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{
		CallId: id, Name: name, Output: []*aop.Content{aop.Text(result)}, IsError: isError,
	}}}
}

func usageEvent(input, outputTok, total int) *aop.Event {
	return &aop.Event{TurnId: "run-test", Payload: &aop.Event_Usage{Usage: &aop.TokenUsage{
		InputTokens: uint64(input), OutputTokens: uint64(outputTok), TotalTokens: uint64(total),
	}}}
}

func testOutput(stderr io.Writer, verbosity int, debug bool) *AgentOutput {
	stdout := &bytes.Buffer{}
	color := output.NewColor(false)
	policy := cfg.OutputPolicyForLevel(verbosity)
	o := &AgentOutput{
		color:     color,
		debug:     debug,
		verbosity: verbosity,
		policy:    policy,
		stream:    NewStreamWriter(stdout, stderr, true, false, color, policy.ShowReasoning()),
		deltas:    make(map[string]*deltaAccumulator),
	}
	o.live = NewLiveStatus(NewLiveView(stderr, ""), o.dim, o.renderToolLine)
	o.live.SetUsageVisible(policy.Usage)
	return o
}

func liveRunning(l *LiveStatus) bool {
	return l.Running()
}

func TestRenderAgentMarkdownPlainFallback(t *testing.T) {
	got := renderAgentMarkdown("  ## Title\n\n- item  ", false)
	want := "## Title\n\n- item"
	if got != want {
		t.Fatalf("renderAgentMarkdown() = %q, want %q", got, want)
	}
}

func TestFormatTokenUsageUsesCompactMarkers(t *testing.T) {
	got := formatTokenUsage(provider.TokenUsage(1832, 63, 0, 1026, 0))
	if got != "↑1,832 ↓63 ↻56%" {
		t.Fatalf("formatTokenUsage() = %q", got)
	}
}

func TestAgentOutputFinalWritesPlainMarkdownWithoutWrapper(t *testing.T) {
	var stdout bytes.Buffer
	color := output.NewColor(false)
	o := &AgentOutput{
		color:  color,
		policy: cfg.OutputPolicyForPreset(cfg.OutputPresetDefault),
		stream: NewStreamWriter(&stdout, &bytes.Buffer{}, true, false, color, false),
		deltas: make(map[string]*deltaAccumulator),
	}
	o.live = NewLiveStatus(NewLiveView(&bytes.Buffer{}, ""), o.dim, o.renderToolLine)

	o.Final("## Report\n\nDone.")

	got := stdout.String()
	if !strings.Contains(got, "## Report") || !strings.Contains(got, "Done.") {
		t.Fatalf("final output missing markdown content: %q", got)
	}
}

func TestThinkingSpinnerSurvivesInvisibleStreamUpdates(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if !liveRunning(o.live) {
		t.Fatal("thinking spinner did not start")
	}

	o.HandleEvent(textDeltaEvent("m-1", ""))
	if !liveRunning(o.live) {
		t.Fatal("empty stream update stopped thinking spinner")
	}

	o.HandleEvent(reasoningDeltaEvent("m-1", "internal reasoning that is hidden at default verbosity"))
	if !liveRunning(o.live) {
		t.Fatal("hidden reasoning stream update stopped thinking spinner")
	}

	o.HandleEvent(textDeltaEvent("m-1", "partial paragraph without markdown flush"))
	if !liveRunning(o.live) {
		t.Fatal("buffered markdown stream update stopped thinking spinner before visible output")
	}

	o.HandleEvent(textDeltaEvent("m-1", "\n\n"))
	if !liveRunning(o.live) {
		t.Fatal("visible stream update stopped thinking spinner")
	}
	if !strings.Contains(stdout.String(), "partial paragraph") {
		t.Fatalf("visible content was not written: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestInboxPreviewKeepsThinkingLiveAndTruncates(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.SetInbox([]string{
		strings.Repeat("very long pending prompt ", 10),
		"second pending prompt",
	})

	if !liveRunning(o.live) {
		t.Fatal("inbox update stopped the thinking status")
	}
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "inbox[2]") || !strings.Contains(got, "second pending prompt") {
		t.Fatalf("live inbox preview missing status or content: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("long inbox entry was not truncated: %q", got)
	}
	if strings.Contains(got, "queued:") {
		t.Fatalf("legacy queued line leaked into inbox rendering: %q", got)
	}

	// Starting the next run resets turn state but must retain inputs that are
	// still waiting behind it.
	o.Start("prompt", "")
	o.HandleEvent(turnStartEvent(1))
	if got := stripANSI(stderr.String()); !strings.Contains(got, "inbox[2]") {
		t.Fatalf("inbox preview did not survive run reset: %q", got)
	}
}

func TestNonTTYMessageUpdateBuffersUntilTurnEnd(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, false)

	content := "buffered answer"
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", content))
	if stdout.Len() != 0 {
		t.Fatalf("non-TTY update streamed stdout before turn end: %q", stdout.String())
	}

	o.HandleEvent(messageEvent("m-1", "assistant", aop.Text(content)))
	o.HandleEvent(turnEndEvent(1, 0))
	if !strings.Contains(stdout.String(), content) {
		t.Fatalf("non-TTY turn end did not render content: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStaticOutputDisablesDynamicTUIOnTTY(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewStaticAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if liveRunning(o.live) {
		t.Fatal("static output started thinking live view")
	}

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo hi"}`))
	if liveRunning(o.live) {
		t.Fatal("static output started tool live view")
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "▸") || !strings.Contains(got, "bash") || !strings.Contains(got, "echo hi") {
		t.Fatalf("static tool output missing direct rendering: %q", got)
	}
	if strings.Contains(stderr.String(), syncBegin) || strings.Contains(stderr.String(), eraseLine) {
		t.Fatalf("static output wrote dynamic ANSI controls: %q", stderr.String())
	}
}

func TestThinkingLineShowsTurnUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(1000, 234, 1234))
	o.HandleEvent(textDeltaEvent("m-1", ""))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "thinking") ||
		!strings.Contains(got, "[turn 1 | ↑1,000 ↓234") {
		t.Fatalf("thinking line missing turn usage: %q", got)
	}
	if !liveRunning(o.live) {
		t.Fatal("usage update stopped thinking spinner")
	}
}

func TestLiveStatusCanHideUsageDetails(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	showUsage := false
	o := NewAgentOutputWithWriters(&cfg.Option{
		LLMOptions:    cfg.LLMOptions{Model: "gpt-4"},
		OutputOptions: cfg.OutputOptions{Usage: &showUsage},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(4096, 50, 4146))
	o.HandleEvent(textDeltaEvent("m-1", "12345678"))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "turn 1") {
		t.Fatalf("live status itself was hidden: %q", got)
	}
	for _, hidden := range []string{"↑", "↓", "◐", "4,096/8,192"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("usage detail %q leaked into live status: %q", hidden, got)
		}
	}
}

func TestThinkingLineShowsChangingStreamTokenEstimate(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", "12345678"))
	first := stripANSI(stderr.String())
	if !strings.Contains(first, "↓≈2") {
		t.Fatalf("initial stream token estimate missing: %q", first)
	}

	o.HandleEvent(textDeltaEvent("m-1", "12345678"))
	time.Sleep(readlineFooterInterval + 75*time.Millisecond)
	second := stripANSI(stderr.String())
	if !strings.Contains(second, "↓≈4") {
		t.Fatalf("updated stream token estimate missing: %q", second)
	}

	o.HandleEvent(usageEvent(100, 7, 107))
	exact := stripANSI(stderr.String())
	if strings.LastIndex(exact, "↓7") <= strings.LastIndex(exact, "↓≈4") {
		t.Fatalf("formal usage did not replace the estimate: %q", exact)
	}
}

func TestThinkingLineRefreshesElapsedTimeWithoutHistoryStats(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	time.Sleep(150 * time.Millisecond)

	got := stripANSI(stderr.String())
	if !regexp.MustCompile(`\[turn 1 \| (?:[1-9][0-9]*ms|[1-9][0-9]*\.[0-9]s)\]`).MatchString(got) {
		t.Fatalf("thinking line did not refresh elapsed time: %q", got)
	}
}

func TestReadlineFooterRefreshesAtConfiguredRate(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var mu sync.Mutex
	var statuses []string
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(io.Discard, func(status string) {
		mu.Lock()
		statuses = append(statuses, status)
		mu.Unlock()
	})
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	mu.Lock()
	initial := len(statuses)
	mu.Unlock()
	if initial != 1 {
		t.Fatalf("readline footer rendered %d times at turn start, want exactly 1", initial)
	}

	time.Sleep(250 * time.Millisecond)
	mu.Lock()
	after := len(statuses)
	mu.Unlock()
	if after <= initial {
		t.Fatalf("readline footer did not refresh: before=%d after=%d", initial, after)
	}
}

func TestReadlineFooterCoalescesStreamTokenUpdates(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var mu sync.Mutex
	var statuses []string
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(io.Discard, func(status string) {
		mu.Lock()
		statuses = append(statuses, stripANSI(status))
		mu.Unlock()
	})
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", "12345678"))
	mu.Lock()
	afterFirst := len(statuses)
	mu.Unlock()

	o.HandleEvent(textDeltaEvent("m-1", "12345678"))
	mu.Lock()
	afterSmallUpdate := len(statuses)
	mu.Unlock()
	if afterSmallUpdate != afterFirst {
		t.Fatalf("small token delta forced a footer refresh: before=%d after=%d", afterFirst, afterSmallUpdate)
	}

	o.HandleEvent(textDeltaEvent("m-1", strings.Repeat("x", 512)))
	mu.Lock()
	afterMilestone := len(statuses)
	mu.Unlock()
	if afterMilestone != afterSmallUpdate {
		t.Fatalf("token delta bypassed footer ticker: before=%d after=%d", afterSmallUpdate, afterMilestone)
	}
	time.Sleep(readlineFooterInterval + 75*time.Millisecond)
	mu.Lock()
	afterTick := len(statuses)
	latest := statuses[len(statuses)-1]
	mu.Unlock()
	if afterTick <= afterMilestone {
		t.Fatalf("footer ticker did not publish token update: before=%d after=%d", afterMilestone, afterTick)
	}
	if !strings.Contains(latest, "↓≈") {
		t.Fatalf("token milestone footer missing estimate: %q", latest)
	}
}

func TestInteractiveInputSuppressesLiveStatus(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.SetInteractiveInputActive(true)
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(1000, 234, 1234))
	o.HandleEvent(textDeltaEvent("m-1", "hello"))

	got := stripANSI(stderr.String())
	if strings.Contains(got, "thinking") || strings.Contains(got, "↑1,000 ↓234") {
		t.Fatalf("live status leaked while input active: %q", got)
	}
	if liveRunning(o.live) {
		t.Fatal("live spinner started while input was active")
	}
}

func TestLiveStatusShowsCurrentTurnContextAndOutputTokens(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		LLMOptions: cfg.LLMOptions{Model: "gpt-4"},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(400, 100, 1000))
	o.HandleEvent(turnEndEvent(1, 400))

	o.HandleEvent(turnStartEvent(2))
	o.HandleEvent(usageEvent(4096, 50, 2000))
	o.HandleEvent(textDeltaEvent("m-2", ""))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "turn 2") || !strings.Contains(got, "↑4,096 ↓50") {
		t.Fatalf("live line missing current turn usage: %q", got)
	}
	if !strings.Contains(got, "◐4,096/8,192 (50%)") {
		t.Fatalf("live line missing context percentage: %q", got)
	}
}

func TestTurnStatsStayTransientInStaticMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewStaticAgentOutputWithWriters(&cfg.Option{
		LLMOptions: cfg.LLMOptions{Model: "gpt-4"},
	}, &stdout, &stderr, true)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(usageEvent(4096, 50, 4146))
	o.HandleEvent(turnEndEvent(1, 4096))

	got := stripANSI(stderr.String())
	if strings.Contains(got, "turn 1") || strings.Contains(got, "↑4,096 ↓50") {
		t.Fatalf("turn stats were committed to static output: %q", got)
	}
}

func TestLiveStatusSwitchesTalkingAndTooling(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	if o.live.Status() != liveStatusThinking {
		t.Fatalf("live status = %q, want thinking", o.live.Status())
	}

	o.HandleEvent(textDeltaEvent("m-1", "partial assistant answer"))
	if o.live.Status() != liveStatusTalking {
		t.Fatalf("live status = %q, want talking", o.live.Status())
	}
	if !liveRunning(o.live) {
		t.Fatal("talking should keep using the shared live status row")
	}

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo hi"}`))
	if o.live.Status() != liveStatusTooling {
		t.Fatalf("live status = %q, want tooling", o.live.Status())
	}

	got := stripANSI(stderr.String())
	if !strings.Contains(got, liveStatusTalking) || !strings.Contains(got, liveStatusTooling) {
		t.Fatalf("live output missing status labels: %q", got)
	}
}

func TestReadlineFooterRendersLiveToolLines(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var mu sync.Mutex
	latest := ""
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(io.Discard, func(status string) {
		mu.Lock()
		latest = stripANSI(status)
		mu.Unlock()
	})
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"spray -u https://example.com -j"}`))

	mu.Lock()
	got := latest
	mu.Unlock()
	if !strings.Contains(got, "tooling") || !strings.Contains(got, "bash") ||
		!strings.Contains(got, "spray -u https://example.com -j") {
		t.Fatalf("live tool footer missing progress: %q", got)
	}
	if !strings.Contains(got, "\n") {
		t.Fatalf("tool progress was not rendered on its own composer row: %q", got)
	}
}

func TestReadlineToolSpinnerRefreshesWithoutToolEvents(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var mu sync.Mutex
	frames := make(map[string]struct{})
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(io.Discard, func(status string) {
		plain := stripANSI(status)
		if strings.Contains(plain, "bash") {
			mu.Lock()
			frames[plain] = struct{}{}
			mu.Unlock()
		}
	})
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"sleep 1"}`))
	// The first configured interval must advance the frame. Previously the
	// ticker repainted frame zero once, so the first visible change took two
	// intervals and made tool progress look event-driven or sluggish.
	time.Sleep(readlineFooterInterval + 75*time.Millisecond)

	mu.Lock()
	count := len(frames)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("tool spinner produced %d distinct frames, want at least 2", count)
	}
}

func TestThinkingVerboseStreamsReasoningWithoutTags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	reasoning := "checking target scope\nprobing admin route"
	o.HandleEvent(reasoningDeltaEvent("m-1", reasoning))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "checking target scope") || !strings.Contains(got, "probing admin route") {
		t.Fatalf("streamed thinking block missing reasoning: %q", got)
	}
	if !liveRunning(o.live) {
		t.Fatal("thinking spinner stopped while reasoning was streamed")
	}
	if strings.Contains(stderr.String(), "<thinking>") {
		t.Fatalf("reasoning tag was printed: %q", stderr.String())
	}
	if o.stream.ReasoningPrinted() != len(reasoning) {
		t.Fatalf("reasoning printed = %d, want %d", o.stream.ReasoningPrinted(), len(reasoning))
	}
}

func TestThinkingVerboseStreamsOnlyReasoningDelta(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", "The user wants"))
	o.HandleEvent(reasoningDeltaEvent("m-1", " me to test redhaze.top"))

	got := stripANSI(stderr.String())
	if strings.Count(got, "The user wants") != 1 {
		t.Fatalf("reasoning prefix rendered repeatedly: %q", got)
	}
	if !strings.Contains(got, "me to test redhaze.top") {
		t.Fatalf("reasoning delta not streamed correctly: %q", got)
	}
}

func TestReadlineThinkingAppendsWithoutSyntheticNewlines(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		ready:  true,
		commit: func(text string) error {
			committed = append(committed, stripANSI(text))
			return nil
		},
		redraw: func() {},
	}
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true}},
	}, &stdout, &stderr, true)
	o.SetReadlineMode(bridge, bridge.UpdateStatus)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", "The user wants"))
	o.HandleEvent(reasoningDeltaEvent("m-1", " me to inspect the image"))

	if len(committed) != 0 {
		t.Fatalf("partial reasoning was committed as separate lines: %#v", committed)
	}

	o.HandleEvent(reasoningDeltaEvent("m-1", "\nthen report"))
	if len(committed) != 1 || committed[0] != "The user wants me to inspect the image" {
		t.Fatalf("reasoning line commits = %#v", committed)
	}

	reasoning := "The user wants me to inspect the image\nthen report"
	o.HandleEvent(messageEvent("m-1", "assistant", aop.Reasoning(reasoning)))
	o.HandleEvent(turnEndEvent(1, 0))
	if len(committed) != 2 || committed[1] != "then report" {
		t.Fatalf("final reasoning commits = %#v", committed)
	}
}

func TestReadlineDefaultDoesNotCommitThinking(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		ready:  true,
		commit: func(text string) error {
			committed = append(committed, stripANSI(text))
			return nil
		},
		redraw: func() {},
	}
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(bridge, bridge.UpdateStatus)
	defer o.live.Stop()

	reasoning := "private chain of thought\nsecond line"
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", reasoning))
	o.HandleEvent(messageEvent("m-1", "assistant", aop.Reasoning(reasoning)))
	o.HandleEvent(turnEndEvent(1, 0))

	if joined := strings.Join(committed, "\n"); strings.Contains(joined, "private chain of thought") {
		t.Fatalf("default verbosity committed thinking: %#v", committed)
	}
}

func TestReadlineShowsAndCommitsIntermediateAssistantTextBeforeTool(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		ready:  true,
		commit: func(text string) error {
			committed = append(committed, stripANSI(text))
			return nil
		},
		redraw: func() {},
	}
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(bridge, bridge.UpdateStatus)
	defer o.live.Stop()

	text := "I will inspect the image before running the scanner."
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", text))

	o.HandleEvent(messageEvent("m-1", "assistant", aop.Text(text)))
	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"scan image.png"}`))
	if len(committed) != 1 || !strings.Contains(committed[0], text) {
		t.Fatalf("intermediate assistant text was not committed before tool: %#v", committed)
	}
}

func TestReadlineCommitsFinalTextForImageResponse(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		ready:  true,
		commit: func(text string) error {
			committed = append(committed, stripANSI(text))
			return nil
		},
		redraw: func() {},
	}
	o := NewAgentOutputWithWriters(&cfg.Option{}, &stdout, &stderr, true)
	o.SetReadlineMode(bridge, bridge.UpdateStatus)
	defer o.live.Stop()

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(messageEvent("m-1", "assistant",
		aop.Text("The screenshot shows an exposed admin login."),
		aop.Text("No credentials are visible."),
	))
	o.HandleEvent(turnEndEvent(1, 0))

	joined := strings.Join(committed, "\n")
	if !strings.Contains(joined, "The screenshot shows an exposed admin login.") ||
		!strings.Contains(joined, "No credentials are visible.") {
		t.Fatalf("image response text missing from readline output: %#v", committed)
	}
}

func TestThinkingBlockFinalRenderingHasNoTags(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)
	reasoning := "checking target scope\nprobing admin route"

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(messageEvent("m-1", "assistant", aop.Reasoning(reasoning)))
	o.HandleEvent(turnEndEvent(1, 0))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "checking target scope") || !strings.Contains(got, "probing admin route") {
		t.Fatalf("final thinking block missing reasoning: %q", got)
	}
	if strings.Contains(got, "<thinking>") || strings.Contains(got, "</thinking>") {
		t.Fatalf("final thinking block contains tags: %q", got)
	}
}

func TestAgentOutputToolSummary(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"scan -i 127.0.0.1 --mode quick"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "ok", false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "bash") || !strings.Contains(got, "scan -i 127.0.0.1 --mode quick") {
		t.Fatalf("stderr missing tool summary: %q", got)
	}
	if !strings.Contains(got, "▸") {
		t.Fatalf("stderr missing ▸ start marker: %q", got)
	}
	if !strings.Contains(got, "✓") {
		t.Fatalf("stderr missing ✓ end marker: %q", got)
	}
	if !strings.Contains(got, "command") {
		t.Fatalf("stderr missing structured arg key 'command': %q", got)
	}
}

func TestAgentOutputToolDebugDetails(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, true)

	o.HandleEvent(toolCallEvent("call-1", "read", `{"path":"docs/usage.md","limit":20}`))
	o.HandleEvent(toolResultEvent("call-1", "read", "file content", false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "read") || !strings.Contains(got, "docs/usage.md") {
		t.Fatalf("stderr missing read summary: %q", got)
	}
	if !strings.Contains(got, `raw: {`) || !strings.Contains(got, `"path":"docs/usage.md"`) || !strings.Contains(got, `"limit":20`) {
		t.Fatalf("stderr missing compact args in debug mode: %q", got)
	}
	if !strings.Contains(got, "file content") {
		t.Fatalf("stderr missing result content in debug mode: %q", got)
	}
}

func TestAgentOutputToolError(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolResultEvent("call-1", "bash", "permission denied", true))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✗") {
		t.Fatalf("stderr missing ✗ error marker: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Fatalf("stderr missing tool error: %q", got)
	}
}

func TestAgentOutputWriteEditSummary(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(toolCallEvent("call-1", "write", `{"path":"src/main.go","edits":[{"old_text":"foo","new_text":"bar"},{"old_text":"baz","new_text":"qux"}]}`))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "▸") {
		t.Fatalf("stderr missing ▸ marker: %q", got)
	}
	if !strings.Contains(got, "src/main.go") {
		t.Fatalf("stderr missing file path: %q", got)
	}
	if !strings.Contains(got, "2 change(s)") {
		t.Fatalf("stderr missing edit count: %q", got)
	}
}

func TestAgentOutputMultiLineResult(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	result := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20"
	o.HandleEvent(toolResultEvent("call-1", "bash", result, false))

	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✓") {
		t.Fatalf("stderr missing ✓ marker: %q", got)
	}
	if !strings.Contains(got, "line1") {
		t.Fatalf("stderr missing first line: %q", got)
	}
	if !strings.Contains(got, "+") && !strings.Contains(got, "lines") {
		t.Fatalf("stderr missing truncation hint for multi-line result: %q", got)
	}
}

func TestAgentOutputFullResultIsNotTruncated(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 2, false)
	result := strings.Join([]string{
		"line1", "line2", "line3", "line4", "line5", "line6", "line7", "line8", "line9", "line10",
		"line11", "line12", "line13", "line14", "line15", "line16", "line17", "line18", "line19", "line20",
	}, "\n")

	o.HandleEvent(toolResultEvent("call-1", "bash", result, false))
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "line20") || strings.Contains(got, "lines hidden") {
		t.Fatalf("full result was truncated: %q", got)
	}
}

func TestAgentOutputDefaultKeepsToolOutputCompact(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo compact"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "sensitive result body", false))
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "bash") || !strings.Contains(got, "echo compact") {
		t.Fatalf("compact summary missing: %q", got)
	}
	if strings.Contains(got, "command  ") || strings.Contains(got, "sensitive result body") {
		t.Fatalf("default output leaked tool detail: %q", got)
	}
}

func TestAgentOutputWithoutLiveStatusKeepsStaticToolSummaries(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	showLive := false
	o := NewAgentOutputWithWriters(&cfg.Option{OutputOptions: cfg.OutputOptions{
		LiveStatus: &showLive,
	}}, &stdout, &stderr, true)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo compact"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "hidden result body", false))

	got := stripANSI(stderr.String())
	if o.canAnimate() || liveRunning(o.live) {
		t.Fatal("live status remained active after output.live_status=false")
	}
	if !strings.Contains(got, "bash") || !strings.Contains(got, "echo compact") || !strings.Contains(got, "✓") {
		t.Fatalf("static compact tool summary missing: %q", got)
	}
	if strings.Contains(got, "thinking") || strings.Contains(got, "hidden result body") {
		t.Fatalf("disabled live status rendered transient or detailed output: %q", got)
	}
}

func TestAgentOutputSeparatesReasoningAndFinalAnswerStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{
		MiscOptions: cfg.MiscOptions{Verbose: []bool{true}},
	}, &stdout, &stderr, true)
	defer o.live.Stop()

	reasoning := "reasoning-stream-only"
	answer := "final-answer-stream-only"
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", reasoning))
	o.HandleEvent(textDeltaEvent("m-1", answer+"\n\n"))
	o.HandleEvent(messageEvent("m-1", "assistant",
		aop.Reasoning(reasoning),
		aop.Text(answer),
	))
	o.HandleEvent(turnEndEvent(1, 0))

	stdoutText := stripANSI(stdout.String())
	stderrText := stripANSI(stderr.String())
	if !strings.Contains(stdoutText, answer) || strings.Contains(stdoutText, reasoning) {
		t.Fatalf("stdout mixed agent streams: %q", stdoutText)
	}
	if !strings.Contains(stderrText, reasoning) || strings.Contains(stderrText, answer) {
		t.Fatalf("stderr mixed agent streams: %q", stderrText)
	}
}

func TestAgentOutputCustomPolicyControlsEachToolSection(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	show := false
	o := NewAgentOutputWithWriters(&cfg.Option{OutputOptions: cfg.OutputOptions{
		Reasoning:     "full",
		ToolCalls:     "compact",
		ToolArguments: "full",
		ToolResults:   "hidden",
		LiveStatus:    &show,
		Usage:         &show,
	}}, &stdout, &stderr, true)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(reasoningDeltaEvent("m-1", "custom reasoning"))
	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo a very long custom command"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "hidden result", false))

	got := stripANSI(stderr.String())
	for _, want := range []string{"custom reasoning", "echo a very long custom command"} {
		if !strings.Contains(got, want) {
			t.Fatalf("custom output missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "hidden result") {
		t.Fatalf("custom output included hidden result: %q", got)
	}
	if o.VerbosityLabel() != "custom" || o.canAnimate() {
		t.Fatalf("custom state label=%q animate=%v", o.VerbosityLabel(), o.canAnimate())
	}
}

func TestAgentOutputHiddenToolsSuppressesArgumentsAndResults(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	o := NewAgentOutputWithWriters(&cfg.Option{OutputOptions: cfg.OutputOptions{
		ToolCalls: "hidden", ToolArguments: "full", ToolResults: "full",
	}}, &stdout, &stderr, false)

	o.HandleEvent(toolCallEvent("call-1", "bash", `{"command":"echo hidden"}`))
	o.HandleEvent(toolResultEvent("call-1", "bash", "hidden result", false))
	if got := stripANSI(stderr.String()); strings.Contains(got, "echo hidden") || strings.Contains(got, "hidden result") {
		t.Fatalf("hidden tool output was rendered: %q", got)
	}
}

func TestAgentOutputCustomPresetCycleStartsAtDefault(t *testing.T) {
	show := false
	o := NewAgentOutputWithWriters(&cfg.Option{OutputOptions: cfg.OutputOptions{
		Reasoning: "full", LiveStatus: &show,
	}}, &bytes.Buffer{}, &bytes.Buffer{}, false)

	if got := o.VerbosityLabel(); got != "custom" {
		t.Fatalf("initial label = %q, want custom", got)
	}
	for _, want := range []string{"default", "thinking", "full", "default"} {
		if got := o.CycleOutputPreset(); got != want {
			t.Fatalf("cycle label = %q, want %q", got, want)
		}
	}
}

func TestFormatToolArguments(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		wantKeys  []string
	}{
		{"bash command", "bash", `{"command":"ls -la"}`, []string{"command"}},
		{"read with offset", "read", `{"path":"main.go","offset":10,"limit":50}`, []string{"path", "offset", "limit"}},
		{"read skips zero offset", "read", `{"path":"main.go","offset":0}`, []string{"path"}},
		{"write with edits", "write", `{"path":"a.go","edits":[{"old_text":"x","new_text":"y"}]}`, []string{"path", "edits"}},
		{"glob", "glob", `{"pattern":"*.go","path":"src/"}`, []string{"pattern", "path"}},
		{"unknown tool uses all keys sorted", "custom", `{"z_key":"z","a_key":"a"}`, []string{"a_key", "z_key"}},
		{"empty args", "bash", `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := formatToolArguments(tt.toolName, tt.arguments)
			if tt.wantKeys == nil {
				if len(lines) != 0 {
					t.Fatalf("expected no lines, got %d", len(lines))
				}
				return
			}
			if len(lines) != len(tt.wantKeys) {
				t.Fatalf("expected %d lines, got %d: %+v", len(tt.wantKeys), len(lines), lines)
			}
			for i, wk := range tt.wantKeys {
				if lines[i].key != wk {
					t.Errorf("line[%d].key = %q, want %q", i, lines[i].key, wk)
				}
			}
		})
	}
}

func TestExtractPseudoCommand(t *testing.T) {
	tests := []struct {
		input      string
		wantTool   string
		wantTarget string
	}{
		{"scan -i 10.0.0.1 --mode quick", "scan", "10.0.0.1"},
		{"gogo -i 10.0.0.0/24 --ports top1000", "gogo", "10.0.0.0/24"},
		{"ls -la", "", ""},
		{"neutron http://target.com", "neutron", "http://target.com"},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tool, target := extractPseudoCommand(tt.input)
			if tool != tt.wantTool {
				t.Errorf("tool = %q, want %q", tool, tt.wantTool)
			}
			if target != tt.wantTarget {
				t.Errorf("target = %q, want %q", target, tt.wantTarget)
			}
		})
	}
}

func TestToolCallCounting(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(toolResultEvent("c1", "bash", "ok", false))
	o.HandleEvent(toolResultEvent("c2", "read", "data", false))
	o.HandleEvent(toolResultEvent("c3", "bash", "fail", true))

	if o.toolCallCount != 3 {
		t.Errorf("toolCallCount = %d, want 3", o.toolCallCount)
	}
	if o.toolErrorCount != 1 {
		t.Errorf("toolErrorCount = %d, want 1", o.toolErrorCount)
	}
}

func TestTurnStartDoesNotWritePermanentMarker(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	o.HandleEvent(turnStartEvent(1))
	turn1Output := stderr.String()

	o.HandleEvent(turnStartEvent(2))
	turn2Output := stderr.String()[len(turn1Output):]

	got1 := stripANSI(turn1Output)
	if strings.Contains(got1, "turn 1") {
		t.Fatalf("turn 1 should not show turn marker, got: %q", got1)
	}

	got2 := stripANSI(turn2Output)
	if strings.Contains(got2, "turn 2") {
		t.Fatalf("turn 2 marker should stay transient, got: %q", got2)
	}
}

func TestEvalEndRendering(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 1, false)

	passed := &aop.Event{Payload: &aop.Event_Status{Status: &aop.Status{State: ext.EvalStateEnd}}}
	_ = ext.SetEvalDetail(passed, ext.EvalDetail{Round: 1, Pass: true, Reason: "all checks passed"})
	o.HandleEvent(passed)
	got := stripANSI(stderr.String())
	if !strings.Contains(got, "✓") || !strings.Contains(got, "eval") || !strings.Contains(got, "pass") {
		t.Fatalf("eval pass missing expected markers: %q", got)
	}
	if !strings.Contains(got, "round 1") {
		t.Fatalf("eval pass used wrong round: %q", got)
	}
	if !strings.Contains(got, "all checks passed") {
		t.Fatalf("eval pass missing reason: %q", got)
	}

	stderr.Reset()
	failed := &aop.Event{Payload: &aop.Event_Status{Status: &aop.Status{State: ext.EvalStateEnd}}}
	_ = ext.SetEvalDetail(failed, ext.EvalDetail{Round: 2, Pass: false, Reason: "port 443 not scanned"})
	o.HandleEvent(failed)
	got = stripANSI(stderr.String())
	if !strings.Contains(got, "⟳") || !strings.Contains(got, "fail") {
		t.Fatalf("eval fail missing expected markers: %q", got)
	}
	if !strings.Contains(got, "round 2") {
		t.Fatalf("eval fail used wrong round: %q", got)
	}
}

func TestLiveStatusEvalRoundUsesProtocolValue(t *testing.T) {
	live := &LiveStatus{}
	live.ShowEvalRound(1)
	if live.note != "eval · round 1" {
		t.Fatalf("eval live status used wrong round: %q", live.note)
	}
}

func TestCompleteMessageClearsDeltaAccumulator(t *testing.T) {
	var stderr syncedBuffer
	o := testOutput(&stderr, 0, false)

	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("m-1", "hello"))
	o.HandleEvent(messageEvent("m-1", "assistant", aop.Text("hello")))
	if len(o.deltas) != 0 {
		t.Fatalf("delta accumulator not cleared on complete message: %d entries", len(o.deltas))
	}
	if !o.hasAssistant {
		t.Fatal("complete assistant message not recorded")
	}
}
