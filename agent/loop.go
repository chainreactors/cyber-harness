package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func runLoop(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent provider is nil")
	}
	if cfg.Tools == nil {
		cfg.Tools = tool.EmptyExecutor()
	}

	transcript := newTranscript(cfg.Messages, 8)
	turn := 0
	overflowRecoveryAttempted := false

	em := cfg.emitter
	ib := cfg.Inbox
	ended := false
	end := func(result *Result, err error, stop StopReason) (*Result, error) {
		if result == nil {
			result = transcript.result("", transcript.completedTurns, err)
		}
		if err != nil && result.Err == nil {
			result.Err = err
		}
		result.Stop = stop
		result.MessageCounter = em.messageCounter()
		if !ended {
			ended = true
			if result.Err != nil && stop == StopReasonError {
				em.errorEvt(result.Err, isRetryableError(result.Err))
			}
			emitRunEnd(ctx, cfg, result)
			if cfg.OnRunEnd != nil {
				cfg.OnRunEnd(result)
			}
		}
		return result, err
	}

	initialPrompt, prepend := runStartHook(ctx, cfg, cfg.SystemPrompt)
	cfg.SystemPrompt = initialPrompt
	transcript.append(prepend...)

	for turn = 1; ; turn++ {
		if err := ctx.Err(); err != nil {
			failure := &aop.Message{Role: "assistant"}
			transcript.append(failure)
			return end(nil, err, StopReasonCanceled)
		}
		if ib != nil {
			inboxMsgs := ib.Drain()
			for i, msg := range inboxMsgs {
				if cfg.Expander != nil {
					inboxMsgs[i] = cfg.Expander.Expand(msg)
				}
				for _, cm := range inboxMsgs[i].ToMessages() {
					transcript.append(cm)
					if inboxMsgs[i].Origin == inbox.OriginUser {
						if cm.Id != "" {
							em.messageWithIdentity(cm.Id, cm.Role, cm.Name, cm.Content)
						} else {
							em.message(cm.Role, cm.Content)
						}
					}
				}
			}
			if len(inboxMsgs) > 0 {
				cfg.Logger.Debugf("[turn %d] drained %d inbox message(s)", turn, len(inboxMsgs))
			}
			if ib.Closed() {
				ib = nil
			}
		}
		systemPrompt := cfg.SystemPrompt
		if cfg.SystemPromptFn != nil {
			systemPrompt = cfg.SystemPromptFn(&cfg)
		}
		reqMessages := requestMessages(ctx, cfg, systemPrompt, transcript.messages, turn)
		toolDefinitions := cfg.Tools.ToolDefinitions()
		contextTokens := transcript.estimatedContextTokens(estimateRequestTokens(reqMessages, toolDefinitions))
		if shouldCompactContext(contextTokens, cfg.ContextWindow, cfg.Compaction) {
			compacted, compactErr := runAutoCompaction(ctx, cfg, em, transcript, "threshold", contextTokens)
			if compactErr != nil {
				cfg.Logger.Warnf("auto-compaction failed: %s", compactErr)
			} else if compacted {
				reqMessages = requestMessages(ctx, cfg, systemPrompt, transcript.messages, turn)
			}
		}
		cfg.Logger.Debugf("[turn %d] sending %d messages to LLM", turn, len(reqMessages))

		assistant, usage, err := requestWithRetry(ctx, cfg, em, reqMessages, toolDefinitions, turn)
		transcript.recordTurnUsage(turn, usage)
		if err != nil {
			if ctx.Err() != nil {
				return end(nil, ctx.Err(), StopReasonCanceled)
			}
			if isContextOverflowError(err) && !overflowRecoveryAttempted {
				compacted, compactErr := runAutoCompaction(ctx, cfg, em, transcript, "overflow", transcript.contextTokens)
				if compactErr != nil {
					cfg.Logger.Warnf("context overflow recovery failed: %s", compactErr)
				} else if compacted {
					overflowRecoveryAttempted = true
					turn--
					continue
				}
			}
			transcript.completedTurns = turn
			return end(nil, err, StopReasonError)
		}
		assistant.normalize()
		if isLengthContextOverflow(assistant.finishReason, usage, cfg.ContextWindow) {
			if !overflowRecoveryAttempted {
				compacted, compactErr := runAutoCompaction(ctx, cfg, em, transcript, "overflow", transcript.contextTokens)
				if compactErr != nil {
					cfg.Logger.Warnf("length overflow recovery failed: %s", compactErr)
				} else if compacted {
					overflowRecoveryAttempted = true
					turn--
					continue
				}
			}
			promptTokens := 0
			if usage != nil {
				promptTokens = int(usage.InputTokens)
			}
			overflowErr := fmt.Errorf("LLM context overflow at turn %d (finish_reason=%s, prompt_tokens=%d)",
				turn, assistant.finishReason, promptTokens)
			transcript.completedTurns = turn
			return end(nil, overflowErr, StopReasonError)
		}
		overflowRecoveryAttempted = false
		if cfg.TokenBudget > 0 && transcript.totalUsage.GetTotalTokens() >= uint64(cfg.TokenBudget) && len(assistant.toolCalls) > 0 {
			cfg.Logger.Warnf("token budget exhausted: %d/%d", transcript.totalUsage.GetTotalTokens(), cfg.TokenBudget)
			result := transcript.result(provider.MessageText(assistant.message), turn, fmt.Errorf("token budget exhausted: %d/%d", transcript.totalUsage.GetTotalTokens(), cfg.TokenBudget))
			return end(result, result.Err, StopReasonBudget)
		}
		transcript.append(assistant.message)

		if cfg.TokenBudget > 0 {
			if transcript.totalUsage.GetTotalTokens() >= uint64(cfg.TokenBudget) {
				cfg.Logger.Warnf("token budget exhausted: %d/%d", transcript.totalUsage.GetTotalTokens(), cfg.TokenBudget)
				result := transcript.result(provider.MessageText(assistant.message), turn, fmt.Errorf("token budget exhausted: %d/%d", transcript.totalUsage.GetTotalTokens(), cfg.TokenBudget))
				return end(result, result.Err, StopReasonBudget)
			}
			if transcript.totalUsage.GetTotalTokens() >= uint64(cfg.TokenBudget)*DefaultTokenBudgetWarningPct/100 {
				em.status(statusTokenBudgetWarning, &types.BudgetWarning{
					ContextTokens: uint64(max(transcript.contextTokens, 0)), TokenBudget: uint64(max(cfg.TokenBudget, 0)),
				})
				cfg.Logger.Warnf("token budget warning: %d/%d (80%%)", transcript.totalUsage.GetTotalTokens(), cfg.TokenBudget)
			}
		}
		var toolResults []*aop.Message
		terminate := false
		if len(assistant.toolCalls) > 0 {
			cfg.Messages = append([]*aop.Message(nil), transcript.messages...)
			batch, err := executeToolCalls(ctx, cfg, em, assistant, turn)
			if err != nil {
				if ctx.Err() != nil {
					return end(nil, ctx.Err(), StopReasonCanceled)
				}
				return end(nil, err, StopReasonError)
			}
			toolResults = batch.messages
			terminate = batch.terminate
			transcript.append(toolResults...)
		}

		em.usage(usage, cfg.Model)
		transcript.completedTurns = turn

		if cfg.MaxTurns > 0 && turn >= cfg.MaxTurns {
			cfg.Logger.Debugf("agent status=stopped turns=%d/%d tokens=%d", turn, cfg.MaxTurns, transcript.totalUsage.GetTotalTokens())
			result := transcript.result(provider.MessageText(assistant.message), turn, nil)
			return end(result, nil, StopReasonStopped)
		}

		if terminate {
			cfg.Logger.Debugf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.GetTotalTokens())
			result := transcript.result(provider.MessageText(assistant.message), turn, nil)
			return end(result, nil, StopReasonTerminated)
		}
		if len(assistant.toolCalls) == 0 {
			if ib != nil && ib.Len() > 0 {
				cfg.Logger.Debugf("[turn %d] continuing for pending inbox message(s)", turn)
				continue
			}

			alive := (cfg.LoopScheduler != nil && cfg.LoopScheduler.Active() > 0) ||
				(ib != nil && ib.ActiveProducers() > 0)

			if alive && ib != nil && !ib.Closed() {
				cfg.Logger.Debugf("[turn %d] waiting for inbox (loops=%d producers=%d)",
					turn, schedulerActive(cfg.LoopScheduler), ib.ActiveProducers())
				hasMessage := ib.Wait(ctx)
				if hasMessage {
					continue
				}
			}

			cfg.Logger.Debugf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.GetTotalTokens())
			result := transcript.result(provider.MessageText(assistant.message), turn, nil)
			return end(result, nil, StopReasonCompleted)
		}
	}

}

// assistantTurn carries one model response: the aop message, its tool calls in
// execution order, and response metadata that has no place in the proto.
type assistantTurn struct {
	message      *aop.Message
	toolCalls    []*aop.ToolCall
	finishReason string
	rejected     map[int]string // tool call index → rejection reason
}

func (a *assistantTurn) normalize() {
	if a.message == nil {
		a.message = &aop.Message{Role: "assistant"}
	}
	if a.message.Role == "" {
		a.message.Role = "assistant"
	}
	a.toolCalls = provider.MessageToolCalls(a.message)
	a.rejected = nil
	if len(a.toolCalls) == 0 {
		return
	}
	truncated := isOutputLimitFinishReason(a.finishReason)
	for i, call := range a.toolCalls {
		rejected := truncated
		reason := truncatedToolCallError
		call.Id = strings.TrimSpace(call.Id)
		call.Name = strings.TrimSpace(call.Name)
		arguments := ""
		if call.Arguments != nil {
			arguments = strings.TrimSpace(string(call.Arguments.Data))
		}
		if arguments == "" {
			arguments = "{}"
		}
		call.Arguments = &aop.EncodedValue{Data: []byte(arguments), MediaType: aop.JSONMediaType}
		if !rejected {
			var args map[string]any
			if call.Id == "" || call.Name == "" ||
				json.Unmarshal([]byte(arguments), &args) != nil || args == nil {
				rejected = true
				reason = invalidToolCallError
			}
		}
		if !rejected {
			if call.Kind == "" {
				call.Kind = "function"
			}
			continue
		}
		if call.Id == "" {
			call.Id = fmt.Sprintf("rejected_tool_call_%d", i+1)
		}
		if call.Kind == "" {
			call.Kind = "function"
		}
		if call.Name == "" {
			call.Name = "unknown_tool"
		}
		// Invalid JSON would poison the next Anthropic request during history
		// serialization. Rejected arguments are never safe to execute or retain.
		call.Arguments = &aop.EncodedValue{Data: []byte("{}"), MediaType: aop.JSONMediaType}
		if a.rejected == nil {
			a.rejected = make(map[int]string)
		}
		a.rejected[i] = reason
	}
}

type transcript struct {
	messages          []*aop.Message
	newMessages       []*aop.Message
	completedTurns    int
	turnUsages        []*aop.TokenUsage
	totalUsage        *aop.TokenUsage
	contextTokens     int
	usageMessageCount int
}

func newTranscript(base []*aop.Message, newCapacity int) *transcript {
	return &transcript{
		messages:    append([]*aop.Message(nil), base...),
		newMessages: make([]*aop.Message, 0, newCapacity),
		totalUsage:  &aop.TokenUsage{Detail: map[string]uint64{}},
	}
}

func (t *transcript) append(messages ...*aop.Message) {
	t.messages = append(t.messages, messages...)
	t.newMessages = append(t.newMessages, messages...)
}

func (t *transcript) replace(messages []*aop.Message, contextTokens int) {
	t.messages = append([]*aop.Message(nil), messages...)
	t.contextTokens = contextTokens
	t.usageMessageCount = len(messages)
}

func (t *transcript) estimatedContextTokens(fallback int) int {
	if t.contextTokens <= 0 {
		return fallback
	}
	estimated := t.contextTokens
	start := t.usageMessageCount
	if start < 0 || start > len(t.messages) {
		start = len(t.messages)
	}
	estimated += estimateAllTokens(t.messages[start:])
	if fallback > estimated {
		return fallback
	}
	return estimated
}

func shouldCompactContext(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if contextWindow <= 0 {
		return false
	}
	reserve, _ := effectiveCompactionLimits(contextWindow, settings)
	return contextTokens > contextWindow-reserve
}

func runAutoCompaction(ctx context.Context, cfg Config, em *aopEmitter, transcript *transcript, reason string, contextTokens int) (bool, error) {
	reserve, keepRecent := effectiveCompactionLimits(cfg.ContextWindow, cfg.Compaction)
	if len(transcript.messages) < 2 || findCutPoint(transcript.messages, keepRecent) <= 0 {
		return false, nil
	}
	if canceled, hookReason := compactCanceled(ctx, cfg, reason, contextTokens); canceled {
		cfg.Logger.Debugf("compaction canceled by hook: %s", hookReason)
		return false, nil
	}

	em.status(types.CompactStateStart, nil)
	newMessages, result, err := compactHistory(ctx, CompactConfig{
		Provider:         cfg.Provider,
		Model:            cfg.Model,
		KeepRecentTokens: keepRecent,
		ReserveTokens:    reserve,
		MaxTokens:        cfg.MaxTokens,
	}, transcript.messages)
	if err != nil {
		em.status(types.CompactStateError, &types.CompactDetail{Error: err.Error()})
		return false, err
	}
	transcript.replace(newMessages, result.TokensAfter)
	em.status(types.CompactStateEnd, &types.CompactDetail{
		TokensBefore: uint64(max(result.TokensBefore, 0)),
		TokensAfter:  uint64(max(result.TokensAfter, 0)),
		KeptMessages: uint64(max(result.KeptMessages, 0)),
	})
	cfg.Logger.Importantf("context compacted reason=%s tokens=%d->%d kept_messages=%d",
		reason, result.TokensBefore, result.TokensAfter, result.KeptMessages)
	return true, nil
}

func effectiveCompactionLimits(contextWindow int, settings CompactionSettings) (reserve, keepRecent int) {
	reserve = settings.ReserveTokens
	if reserve <= 0 {
		reserve = DefaultCompactionReserve
	}
	keepRecent = settings.KeepRecentTokens
	if keepRecent <= 0 {
		keepRecent = DefaultKeepRecentTokens
	}
	if contextWindow <= 0 {
		return reserve, keepRecent
	}
	if limit := contextWindow / 4; limit > 0 && reserve > limit {
		reserve = limit
	}
	if limit := contextWindow / 2; limit > 0 && keepRecent > limit {
		keepRecent = limit
	}
	return reserve, keepRecent
}

func (t *transcript) recordTurnUsage(turn int, usage *aop.TokenUsage) {
	if usage == nil {
		return
	}
	t.turnUsages = append(t.turnUsages, usage)
	t.totalUsage.InputTokens += usage.InputTokens
	t.totalUsage.OutputTokens += usage.OutputTokens
	t.totalUsage.TotalTokens += usage.TotalTokens
	t.totalUsage.Detail["cache_read"] += usage.Detail["cache_read"]
	t.totalUsage.Detail["cache_write"] += usage.Detail["cache_write"]
	t.contextTokens = provider.UsageTotalTokens(usage)
	// Provider usage covers the request plus the assistant response that will be
	// appended immediately after this call.
	t.usageMessageCount = len(t.messages) + 1
}

func (t *transcript) snapshot() ([]*aop.Message, []*aop.Message) {
	return append([]*aop.Message(nil), t.messages...), append([]*aop.Message(nil), t.newMessages...)
}

func (t *transcript) result(output string, turns int, err error) *Result {
	messages, newMessages := t.snapshot()
	return &Result{
		Output:        output,
		NewMessages:   newMessages,
		Messages:      messages,
		Turns:         turns,
		TotalUsage:    t.totalUsage,
		TurnUsages:    append([]*aop.TokenUsage(nil), t.turnUsages...),
		ContextTokens: t.contextTokens,
		Err:           err,
	}
}

type toolBatchResult struct {
	messages  []*aop.Message
	terminate bool
}

func executeToolCalls(ctx context.Context, cfg Config, em *aopEmitter, assistant *assistantTurn, turn int) (toolBatchResult, error) {
	toolCalls := assistant.toolCalls
	slots := make([]toolCallSlot, len(toolCalls))

	for i, tc := range toolCalls {
		slots[i] = toolCallSlot{tc: tc, rejectedReason: assistant.rejected[i]}
	}
	for _, tc := range toolCalls {
		em.toolCall(tc)
	}

	sem := make(chan struct{}, cfg.MaxParallelTools)
	var wg sync.WaitGroup
	for i := range slots {
		if slots[i].rejectedReason != "" {
			slots[i].startedAt = time.Now()
			slots[i].result = toolExecution{
				result: slots[i].rejectedReason, rawResult: slots[i].rejectedReason, isError: true,
			}
			cfg.Logger.Warnf("[turn %d] rejected unsafe tool call name=%s reason=%s",
				turn, slots[i].tc.Name, slots[i].rejectedReason)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			slots[i].startedAt = time.Now()
			slots[i].result = runToolCall(ctx, cfg, assistant.message, slots[i].tc, turn)
		}()
	}
	wg.Wait()

	// Emit results in original order.
	messages := make([]*aop.Message, 0, len(slots))
	terminations := 0
	for _, s := range slots {
		em.toolResult(s.tc, s.result.eventContent(), s.result.fullResult, s.result.flow == ToolFlowTerminate, s.result.isError,
			int(time.Since(s.startedAt).Milliseconds()))
		cfg.Logger.Debugf("[turn %d] tool_result name=%s bytes=%d", turn, s.tc.Name, len(s.result.result))
		messages = append(messages, s.result.toMessage(s.tc.Id))
		if s.result.flow == ToolFlowTerminate {
			terminations++
		}
	}
	return toolBatchResult{
		messages:  messages,
		terminate: len(messages) > 0 && terminations == len(messages),
	}, nil
}

const truncatedToolCallError = "Tool call was not executed because the model response was truncated by the output-token limit. Retry the tool call with complete arguments."

const invalidToolCallError = "Tool call was not executed because the model returned incomplete or invalid call metadata. Retry the tool call with a valid ID, name, and complete JSON object arguments."

func isOutputLimitFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}

type toolCallSlot struct {
	tc             *aop.ToolCall
	rejectedReason string
	result         toolExecution
	startedAt      time.Time
}

type toolExecution struct {
	result     string
	rawResult  string
	fullResult *tool.Result
	isError    bool
	err        error
	flow       ToolFlowDecision
}

func runToolCall(ctx context.Context, cfg Config, assistantMsg *aop.Message, tc *aop.ToolCall, turn int) toolExecution {
	startedAt := time.Now()
	toolCtx := output.ContextWithCallID(ctx, tc.Id)
	toolCtx = withToolAgentConfig(toolCtx, cfg)
	toolCtx = inbox.ContextWithInbox(toolCtx, cfg.Inbox)
	execution := beforeToolCall(toolCtx, cfg, assistantMsg, tc)
	if execution.result == "" && !execution.isError {
		arguments := ""
		if tc.Arguments != nil {
			arguments = string(tc.Arguments.Data)
		}
		toolResult, execErr := cfg.Tools.ExecuteTool(toolCtx, tc.Name, arguments)
		if toolResult == nil {
			toolResult = &tool.Result{}
		}
		execution.result = tool.ResultText(toolResult)
		execution.err = execErr
		execution.isError = execErr != nil || toolResult.IsError
		if execErr != nil {
			execution.result = fmt.Sprintf("error: %s", execErr.Error())
			cfg.Logger.Warnf("[turn %d] tool_error name=%s error=%q", turn, tc.Name, execErr.Error())
		}
		if toolResult.Terminate {
			execution.flow = ToolFlowTerminate
		}
		if tool.ResultHasImages(toolResult) || toolResult.Terminate {
			execution.fullResult = toolResult
		}
	}
	if execution.rawResult == "" {
		execution.rawResult = execution.result
	}
	if tr := truncate.Head(execution.result, truncate.Options{MaxBytes: cfg.MaxResultSize}); tr.Truncated {
		execution.result = tr.Content + fmt.Sprintf(
			"\n\n[truncated: showing %d/%d lines (%s of %s). Refine your query or use filter/parse tools to access specific parts.]",
			tr.OutputLines, tr.TotalLines, truncate.FormatSize(tr.OutputBytes), truncate.FormatSize(tr.TotalBytes))
	}
	return afterToolCall(toolCtx, cfg, assistantMsg, tc, execution, time.Since(startedAt).Milliseconds())
}

func (e toolExecution) eventContent() []*aop.Content {
	content := []*aop.Content{aop.Text(e.eventResultText())}
	if e.fullResult == nil {
		return content
	}
	for _, block := range e.fullResult.Output {
		media := block.GetMedia()
		if media == nil || media.Kind != "image" || media.Resource == nil {
			continue
		}
		if data := media.Resource.GetData(); len(data) > 0 {
			content = append(content, aop.Image(media.Resource.MediaType, data))
		}
	}
	return content
}

func (e toolExecution) eventResultText() string {
	if e.rawResult != "" {
		return e.rawResult
	}
	return e.result
}

// toMessage converts the execution into the tool-role message appended to the
// transcript. Image outputs ride along as media parts; the result text is
// always present so text-only providers keep working.
func (e toolExecution) toMessage(toolCallID string) *aop.Message {
	result := &aop.ToolResult{
		CallId:    toolCallID,
		IsError:   e.isError,
		Terminate: e.flow == ToolFlowTerminate,
	}
	if e.fullResult != nil && tool.ResultHasImages(e.fullResult) {
		for _, block := range e.fullResult.Output {
			if text := block.GetText(); text != nil {
				result.Output = append(result.Output, aop.Text(text.Text))
			}
			if media := block.GetMedia(); media != nil && media.Kind == "image" && media.Resource != nil {
				result.Output = append(result.Output, block)
			}
		}
	} else {
		result.Output = []*aop.Content{aop.Text(e.result)}
	}
	return &aop.Message{Role: "tool", Content: []*aop.Content{{Value: &aop.Content_ToolResult{ToolResult: result}}}}
}

func beforeToolCall(ctx context.Context, cfg Config, assistantMsg *aop.Message, tc *aop.ToolCall) toolExecution {
	if cfg.BeforeToolCall != nil {
		before, err := cfg.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCall:         tc,
			SystemPrompt:     cfg.SystemPrompt,
			Messages:         cfg.Messages,
		})
		if err != nil {
			return toolExecution{result: fmt.Sprintf("error: %s", err.Error()), isError: true, err: err}
		}
		if before != nil && before.Block {
			result := before.Reason
			if result == "" {
				result = "tool execution was blocked"
			}
			return toolExecution{result: result, isError: true}
		}
	}
	return beforeTypedToolCall(ctx, cfg, assistantMsg, tc)
}

func afterToolCall(ctx context.Context, cfg Config, assistantMsg *aop.Message, tc *aop.ToolCall, execution toolExecution, durationMs int64) toolExecution {
	if cfg.AfterToolCall != nil {
		after, err := cfg.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCall:         tc,
			Result:           execution.result,
			IsError:          execution.isError,
			SystemPrompt:     cfg.SystemPrompt,
			Messages:         cfg.Messages,
		})
		if err != nil {
			execution.result = fmt.Sprintf("error: %s", err.Error())
			execution.isError = true
			execution.err = err
			return execution
		}
		if after != nil {
			if after.Result != nil {
				execution.result = *after.Result
			}
			if after.IsError != nil {
				execution.isError = *after.IsError
				if !execution.isError {
					execution.err = nil
				}
			}
			execution.flow = after.Flow
		}
	}
	return afterTypedToolCall(ctx, cfg, tc, execution, int(durationMs))
}

func requestMessages(ctx context.Context, cfg Config, systemPrompt string, messages []*aop.Message, turn int) []*aop.Message {
	out := sanitizeMessages(append([]*aop.Message(nil), messages...))
	if cfg.TransformContext != nil {
		out = cfg.TransformContext(out)
	}
	out = transformContextHook(ctx, cfg, out, turn)
	if systemPrompt != "" {
		out = append([]*aop.Message{provider.TextMessage("system", systemPrompt)}, out...)
	}
	return out
}

func sanitizeMessages(msgs []*aop.Message) []*aop.Message {
	out := make([]*aop.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "assistant" && len(provider.MessageToolCalls(m)) == 0 &&
			provider.MessageText(m) == "" && provider.MessageReasoning(m) == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func logUsage(logger telemetry.Logger, usage *aop.TokenUsage) {
	if usage != nil {
		cacheRead := usage.Detail["cache_read"]
		cacheWrite := usage.Detail["cache_write"]
		if cacheRead > 0 || cacheWrite > 0 {
			logger.Debugf("usage prompt=%d completion=%d total=%d cache_read=%d cache_write=%d",
				usage.InputTokens, usage.OutputTokens, usage.TotalTokens, cacheRead, cacheWrite)
		} else {
			logger.Debugf("usage prompt=%d completion=%d total=%d",
				usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
		}
	}
}

func schedulerActive(s *LoopScheduler) int {
	if s == nil {
		return 0
	}
	return s.Active()
}

// messageBuilder accumulates streamed deltas into one assistant message.
type messageBuilder struct {
	role      string
	content   strings.Builder
	reasoning strings.Builder
	toolCalls map[int]*streamedToolCall
}

type streamedToolCall struct {
	id        string
	kind      string
	name      string
	arguments strings.Builder
}

func newMessageBuilder() *messageBuilder {
	return &messageBuilder{
		role:      "assistant",
		toolCalls: make(map[int]*streamedToolCall),
	}
}

func (b *messageBuilder) Apply(event ChatCompletionStreamEvent) {
	if event.Role != "" {
		b.role = event.Role
	}
	if delta := event.MessageDelta; delta != nil {
		switch value := delta.Value.(type) {
		case *aop.MessageDelta_Text:
			b.content.WriteString(value.Text)
		case *aop.MessageDelta_Reasoning:
			b.reasoning.WriteString(value.Reasoning)
		}
	}
	for _, tcDelta := range event.ToolDeltas {
		index := int(tcDelta.Index)
		tc := b.toolCalls[index]
		if tc == nil {
			tc = &streamedToolCall{kind: "function"}
			b.toolCalls[index] = tc
		}
		if tcDelta.CallId != "" {
			tc.id = tcDelta.CallId
		}
		if tcDelta.Name != "" {
			tc.name = tcDelta.Name
		}
		if len(tcDelta.Arguments) > 0 {
			tc.arguments.Write(tcDelta.Arguments)
		}
	}
}

func (b *messageBuilder) Message() *aop.Message {
	msg := &aop.Message{Role: b.role}
	if reasoning := b.reasoning.String(); reasoning != "" {
		msg.Content = append(msg.Content, aop.Reasoning(reasoning))
	}
	if content := b.content.String(); content != "" {
		msg.Content = append(msg.Content, aop.Text(content))
	}
	if len(b.toolCalls) > 0 {
		indexes := make([]int, 0, len(b.toolCalls))
		for index := range b.toolCalls {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			tc := b.toolCalls[index]
			kind := tc.kind
			if kind == "" {
				kind = "function"
			}
			msg.Content = append(msg.Content, &aop.Content{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
				Id:   tc.id,
				Name: tc.name,
				Kind: kind,
				Arguments: &aop.EncodedValue{
					Data:      []byte(tc.arguments.String()),
					MediaType: aop.JSONMediaType,
				},
			}}})
		}
	}
	return msg
}

// decodeToolArguments renders a tool call's arguments as a JSON value for
// event payloads.
func decodeToolArguments(call *aop.ToolCall) any {
	if call == nil || call.Arguments == nil || len(call.Arguments.Data) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(call.Arguments.Data, &m); err == nil {
		return m
	}
	return string(call.Arguments.Data)
}
