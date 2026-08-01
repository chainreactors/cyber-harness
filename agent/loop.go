package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	aop "github.com/chainreactors/aiscan/aop"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
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
			failure := NewTextMessage("assistant", "")
			transcript.append(failure)
			return end(nil, err, StopReasonCanceled)
		}
		if ib != nil {
			inboxMsgs := ib.Drain()
			for i, msg := range inboxMsgs {
				if cfg.Expander != nil {
					inboxMsgs[i] = cfg.Expander.Expand(msg)
				}
				for _, cm := range inboxMsgs[i].ToChatMessages() {
					transcript.append(cm)
					if inboxMsgs[i].Origin == inbox.OriginUser {
						if cm.AOPMessageID != "" {
							em.messageWithIdentity(cm.AOPMessageID, cm.Role, cm.Name, messagePartsFromChat(cm))
						} else {
							em.message(cm.Role, messagePartsFromChat(cm))
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

		assistantMsg, usage, err := requestWithRetry(ctx, cfg, em, reqMessages, toolDefinitions, turn)
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
		assistantMsg = normalizeToolCalls(assistantMsg)
		if isLengthContextOverflow(assistantMsg.FinishReason, usage, cfg.ContextWindow) {
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
				promptTokens = usage.PromptTokens
			}
			overflowErr := fmt.Errorf("LLM context overflow at turn %d (finish_reason=%s, prompt_tokens=%d)",
				turn, assistantMsg.FinishReason, promptTokens)
			transcript.completedTurns = turn
			return end(nil, overflowErr, StopReasonError)
		}
		overflowRecoveryAttempted = false
		if cfg.TokenBudget > 0 && transcript.totalUsage.TotalTokens >= cfg.TokenBudget && len(assistantMsg.ToolCalls) > 0 {
			cfg.Logger.Warnf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget)
			result := transcript.result(messageContent(assistantMsg), turn, fmt.Errorf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget))
			return end(result, result.Err, StopReasonBudget)
		}
		transcript.append(assistantMsg)

		if cfg.TokenBudget > 0 {
			if transcript.totalUsage.TotalTokens >= cfg.TokenBudget {
				cfg.Logger.Warnf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget)
				result := transcript.result(messageContent(assistantMsg), turn, fmt.Errorf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget))
				return end(result, result.Err, StopReasonBudget)
			}
			if transcript.totalUsage.TotalTokens >= cfg.TokenBudget*DefaultTokenBudgetWarningPct/100 {
				em.status(statusTokenBudgetWarning, aopStatusNamespace, &transport.BudgetWarning{
					ContextTokens: uint64(max(transcript.contextTokens, 0)), TokenBudget: uint64(max(cfg.TokenBudget, 0)),
				})
				cfg.Logger.Warnf("token budget warning: %d/%d (80%%)", transcript.totalUsage.TotalTokens, cfg.TokenBudget)
			}
		}
		var toolResults []ChatMessage
		terminate := false
		if len(assistantMsg.ToolCalls) > 0 {
			cfg.Messages = append([]ChatMessage(nil), transcript.messages...)
			batch, err := executeToolCalls(ctx, cfg, em, assistantMsg, turn)
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
			cfg.Logger.Debugf("agent status=stopped turns=%d/%d tokens=%d", turn, cfg.MaxTurns, transcript.totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			return end(result, nil, StopReasonStopped)
		}

		if terminate {
			cfg.Logger.Debugf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			return end(result, nil, StopReasonTerminated)
		}
		if len(assistantMsg.ToolCalls) == 0 {
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

			cfg.Logger.Debugf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			return end(result, nil, StopReasonCompleted)
		}
	}

}

type transcript struct {
	messages          []ChatMessage
	newMessages       []ChatMessage
	completedTurns    int
	turnUsages        []TurnUsage
	totalUsage        Usage
	contextTokens     int
	usageMessageCount int
}

func newTranscript(base []ChatMessage, newCapacity int) *transcript {
	return &transcript{
		messages:    append([]ChatMessage(nil), base...),
		newMessages: make([]ChatMessage, 0, newCapacity),
	}
}

func (t *transcript) append(messages ...ChatMessage) {
	t.messages = append(t.messages, messages...)
	t.newMessages = append(t.newMessages, messages...)
}

func (t *transcript) replace(messages []ChatMessage, contextTokens int) {
	t.messages = append([]ChatMessage(nil), messages...)
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

	em.status(ext.CompactStateStart, "", nil)
	newMessages, result, err := compactHistory(ctx, CompactConfig{
		Provider:         cfg.Provider,
		Model:            cfg.Model,
		KeepRecentTokens: keepRecent,
		ReserveTokens:    reserve,
		MaxTokens:        cfg.MaxTokens,
	}, transcript.messages)
	if err != nil {
		em.status(ext.CompactStateError, ext.CompactNamespace, &ext.CompactDetail{Error: err.Error()})
		return false, err
	}
	transcript.replace(newMessages, result.TokensAfter)
	em.status(ext.CompactStateEnd, ext.CompactNamespace, &ext.CompactDetail{
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

func (t *transcript) recordTurnUsage(turn int, usage *Usage) {
	if usage == nil {
		return
	}
	t.turnUsages = append(t.turnUsages, TurnUsage{
		Turn:             turn,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usageTotalTokens(usage),
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	})
	t.totalUsage.PromptTokens += usage.PromptTokens
	t.totalUsage.CompletionTokens += usage.CompletionTokens
	t.totalUsage.TotalTokens += usageTotalTokens(usage)
	t.totalUsage.CacheReadTokens += usage.CacheReadTokens
	t.totalUsage.CacheWriteTokens += usage.CacheWriteTokens
	t.contextTokens = usageTotalTokens(usage)
	// Provider usage covers the request plus the assistant response that will be
	// appended immediately after this call.
	t.usageMessageCount = len(t.messages) + 1
}

func (t *transcript) snapshot() ([]ChatMessage, []ChatMessage) {
	return append([]ChatMessage(nil), t.messages...), append([]ChatMessage(nil), t.newMessages...)
}

func (t *transcript) result(output string, turns int, err error) *Result {
	messages, newMessages := t.snapshot()
	return &Result{
		Output:        output,
		NewMessages:   newMessages,
		Messages:      messages,
		Turns:         turns,
		TotalUsage:    t.totalUsage,
		TurnUsages:    append([]TurnUsage(nil), t.turnUsages...),
		ContextTokens: t.contextTokens,
		Err:           err,
	}
}

type toolBatchResult struct {
	messages  []ChatMessage
	terminate bool
}

func executeToolCalls(ctx context.Context, cfg Config, em *aopEmitter, assistantMsg ChatMessage, turn int) (toolBatchResult, error) {
	toolCalls := assistantMsg.ToolCalls
	slots := make([]toolCallSlot, len(toolCalls))

	for i, tc := range toolCalls {
		slots[i] = toolCallSlot{tc: tc}
	}
	for _, tc := range toolCalls {
		em.toolCall(tc.ID, tc.Function.Name, parseToolArgs(tc.Function.Arguments), "")
	}

	sem := make(chan struct{}, cfg.MaxParallelTools)
	var wg sync.WaitGroup
	for i := range slots {
		if slots[i].tc.RejectedReason != "" {
			slots[i].startedAt = time.Now()
			slots[i].result = toolExecution{
				result: slots[i].tc.RejectedReason, rawResult: slots[i].tc.RejectedReason, isError: true,
			}
			cfg.Logger.Warnf("[turn %d] rejected unsafe tool call name=%s reason=%s",
				turn, slots[i].tc.Function.Name, slots[i].tc.RejectedReason)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			slots[i].startedAt = time.Now()
			slots[i].result = runToolCall(ctx, cfg, assistantMsg, slots[i].tc, turn)
		}()
	}
	wg.Wait()

	// Emit results in original order.
	messages := make([]ChatMessage, 0, len(slots))
	terminations := 0
	for _, s := range slots {
		var details any
		if s.result.fullResult != nil {
			details = s.result.fullResult.Details
		}
		em.toolResult(s.tc.ID, s.tc.Function.Name, s.result.eventContent(), details, s.result.flow == ToolFlowTerminate, s.result.isError,
			int(time.Since(s.startedAt).Milliseconds()))
		cfg.Logger.Debugf("[turn %d] tool_result name=%s bytes=%d", turn, s.tc.Function.Name, len(s.result.result))
		toolMsg := toolResultToMessage(s.tc.ID, s.result)
		toolMsg.ToolResultIsError = s.tc.RejectedReason != ""
		messages = append(messages, toolMsg)
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

func normalizeToolCalls(msg ChatMessage) ChatMessage {
	if len(msg.ToolCalls) == 0 {
		return msg
	}
	msg.ToolCalls = append([]ToolCall(nil), msg.ToolCalls...)
	truncated := isOutputLimitFinishReason(msg.FinishReason)
	for i := range msg.ToolCalls {
		tc := &msg.ToolCalls[i]
		rejected := truncated
		reason := truncatedToolCallError
		if tc.RejectedReason != "" {
			rejected = true
			reason = tc.RejectedReason
		}
		tc.ID = strings.TrimSpace(tc.ID)
		tc.Function.Name = strings.TrimSpace(tc.Function.Name)
		arguments := strings.TrimSpace(tc.Function.Arguments)
		if arguments == "" {
			arguments = "{}"
		}
		tc.Function.Arguments = arguments
		if !rejected {
			var args map[string]any
			if tc.ID == "" || tc.Function.Name == "" ||
				json.Unmarshal([]byte(arguments), &args) != nil || args == nil {
				rejected = true
				reason = invalidToolCallError
			}
		}
		if !rejected {
			if tc.Type == "" {
				tc.Type = "function"
			}
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("rejected_tool_call_%d", i+1)
		}
		if tc.Type == "" {
			tc.Type = "function"
		}
		if tc.Function.Name == "" {
			tc.Function.Name = "unknown_tool"
		}
		// Invalid JSON would poison the next Anthropic request during history
		// serialization. Rejected arguments are never safe to execute or retain.
		tc.Function.Arguments = "{}"
		tc.RejectedReason = reason
	}
	return msg
}

type toolCallSlot struct {
	tc        ToolCall
	result    toolExecution
	startedAt time.Time
}

type toolExecution struct {
	result     string
	rawResult  string
	fullResult *tool.Result
	isError    bool
	err        error
	flow       ToolFlowDecision
}

func runToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall, turn int) toolExecution {
	startedAt := time.Now()
	toolCtx := output.ContextWithCallID(ctx, tc.ID)
	toolCtx = withToolAgentConfig(toolCtx, cfg)
	toolCtx = inbox.ContextWithInbox(toolCtx, cfg.Inbox)
	execution := beforeToolCall(toolCtx, cfg, assistantMsg, tc)
	if execution.result == "" && !execution.isError {
		toolResult, execErr := cfg.Tools.ExecuteTool(toolCtx, tc.Function.Name, tc.Function.Arguments)
		execution.result = toolResult.Text()
		execution.err = execErr
		execution.isError = execErr != nil || toolResult.IsError
		if execErr != nil {
			execution.result = fmt.Sprintf("error: %s", execErr.Error())
			cfg.Logger.Warnf("[turn %d] tool_error name=%s error=%q", turn, tc.Function.Name, execErr.Error())
		}
		if toolResult.Terminate {
			execution.flow = ToolFlowTerminate
		}
		if toolResult.HasImages() || toolResult.Details != nil || toolResult.Terminate {
			execution.fullResult = &toolResult
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
	for _, block := range e.fullResult.Content {
		if block.Type != "image" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(block.Base64Data)
		if err == nil {
			content = append(content, aop.Image(block.MimeType, data))
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

func parseToolArgs(raw string) any {
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m
	}
	return raw
}

func toolResultToMessage(toolCallID string, exec toolExecution) ChatMessage {
	if exec.fullResult != nil && exec.fullResult.HasImages() {
		parts := make([]ContentPart, 0, len(exec.fullResult.Content))
		for _, block := range exec.fullResult.Content {
			switch block.Type {
			case "text":
				parts = append(parts, TextPart(block.Text))
			case "image":
				parts = append(parts, ImagePart(block.MimeType, block.Base64Data, "high"))
			}
		}
		return ChatMessage{Role: "tool", ToolCallID: toolCallID, ContentParts: parts}
	}
	return NewToolResultMessage(toolCallID, exec.result)
}

func beforeToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall) toolExecution {
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

func afterToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall, execution toolExecution, durationMs int64) toolExecution {
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

func requestMessages(ctx context.Context, cfg Config, systemPrompt string, messages []ChatMessage, turn int) []ChatMessage {
	out := sanitizeMessages(append([]ChatMessage(nil), messages...))
	if cfg.TransformContext != nil {
		out = cfg.TransformContext(out)
	}
	out = transformContextHook(ctx, cfg, out, turn)
	if systemPrompt != "" {
		out = append([]ChatMessage{NewTextMessage("system", systemPrompt)}, out...)
	}
	return out
}

func sanitizeMessages(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) == 0 &&
			messageContent(m) == "" && len(m.ContentParts) == 0 &&
			(m.ReasoningContent == nil || *m.ReasoningContent == "") {
			continue
		}
		out = append(out, m)
	}
	return out
}

func messageContent(msg ChatMessage) string {
	if msg.Content == nil {
		return ""
	}
	return *msg.Content
}

func logUsage(logger telemetry.Logger, usage *Usage) {
	if usage != nil {
		if usage.CacheReadTokens > 0 || usage.CacheWriteTokens > 0 {
			logger.Debugf("usage prompt=%d completion=%d total=%d cache_read=%d cache_write=%d",
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
				usage.CacheReadTokens, usage.CacheWriteTokens)
		} else {
			logger.Debugf("usage prompt=%d completion=%d total=%d",
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
		}
	}
}

func schedulerActive(s *LoopScheduler) int {
	if s == nil {
		return 0
	}
	return s.Active()
}

type messageBuilder struct {
	role             string
	content          strings.Builder
	reasoningContent strings.Builder
	toolCalls        map[int]*ToolCall
}

func newMessageBuilder() *messageBuilder {
	return &messageBuilder{
		role:      "assistant",
		toolCalls: make(map[int]*ToolCall),
	}
}

func (b *messageBuilder) Apply(delta ChatMessageDelta) ChatMessage {
	if delta.Role != "" {
		b.role = delta.Role
	}
	if delta.Content != nil {
		b.content.WriteString(*delta.Content)
	}
	if delta.ReasoningContent != nil {
		b.reasoningContent.WriteString(*delta.ReasoningContent)
	}
	for _, tcDelta := range delta.ToolCalls {
		tc := b.toolCalls[tcDelta.Index]
		if tc == nil {
			tc = &ToolCall{Type: "function"}
			b.toolCalls[tcDelta.Index] = tc
		}
		if tcDelta.ID != "" {
			tc.ID = tcDelta.ID
		}
		if tcDelta.Type != "" {
			tc.Type = tcDelta.Type
		}
		if tcDelta.Function.Name != "" {
			tc.Function.Name = tcDelta.Function.Name
		}
		if tcDelta.Function.Arguments != "" {
			tc.Function.Arguments += tcDelta.Function.Arguments
		}
	}
	return b.Message()
}

func (b *messageBuilder) Message() ChatMessage {
	content := b.content.String()
	msg := ChatMessage{Role: b.role}
	if content != "" {
		msg.Content = &content
	}
	if reasoningContent := b.reasoningContent.String(); reasoningContent != "" {
		msg.ReasoningContent = &reasoningContent
	}
	if len(b.toolCalls) > 0 {
		indexes := make([]int, 0, len(b.toolCalls))
		for index := range b.toolCalls {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		msg.ToolCalls = make([]ToolCall, 0, len(indexes))
		for _, index := range indexes {
			msg.ToolCalls = append(msg.ToolCalls, *b.toolCalls[index])
		}
	}
	return msg
}
