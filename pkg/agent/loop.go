package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
)

func runLoop(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent provider is nil")
	}
	if cfg.Tools == nil {
		cfg.Tools = commands.NewRegistry()
	}

	fallbacks := cfg.Fallbacks
	fallbackIndex := 0

	transcript := newTranscript(cfg.Messages, 8)
	turn := 0

	bus := newEmitter(cfg.Bus, cfg.SessionID, cfg.ParentSessionID)
	ib := cfg.Inbox
	bus.Emit(Event{Type: EventAgentStart})
	ended := false
	end := func(result *Result, err error, stop StopReason) (*Result, error) {
		if result == nil {
			result = transcript.result("", transcript.completedTurns, err)
		}
		if err != nil && result.Err == nil {
			result.Err = err
		}
		result.Stop = stop
		if !ended {
			ended = true
			bus.Emit(Event{
				Type:        EventAgentEnd,
				Turn:        result.Turns,
				Messages:    append([]ChatMessage(nil), result.Messages...),
				NewMessages: append([]ChatMessage(nil), result.NewMessages...),
				Err:         result.Err,
				Stop:        stop,
			})
		}
		return result, err
	}

	for turn = 1; ; turn++ {
		if err := ctx.Err(); err != nil {
			failure := NewTextMessage("assistant", "")
			transcript.append(failure)
			return end(nil, err, StopReasonCanceled)
		}
		bus.Emit(Event{Type: EventTurnStart, Turn: turn})

		if ib != nil {
			inboxMsgs := ib.Drain()
			for i, msg := range inboxMsgs {
				if cfg.Expander != nil {
					inboxMsgs[i] = cfg.Expander.Expand(msg)
				}
				for _, cm := range inboxMsgs[i].ToChatMessages() {
					transcript.append(cm)
					bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: cm})
					bus.Emit(Event{Type: EventMessageEnd, Turn: turn, Message: cm})
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
		reqMessages := requestMessages(systemPrompt, transcript.messages, cfg.TransformContext)
		cfg.Logger.Debugf("[turn %d] sending %d messages to LLM", turn, len(reqMessages))

		assistantMsg, usage, err := requestWithRetry(ctx, cfg, bus, reqMessages, cfg.Tools.ToolDefinitions(), turn)
		transcript.recordTurnUsage(turn, usage)
		if err != nil {
			if ctx.Err() != nil {
				return end(nil, ctx.Err(), StopReasonCanceled)
			}
			if fallbackIndex < len(fallbacks) {
				next := fallbacks[fallbackIndex]
				fallbackIndex++
				cfg.Logger.Warnf("provider %s exhausted, switching to %s (model %s)",
					cfg.Provider.Name(), next.Provider.Name(), next.Model)
				cfg.Provider = next.Provider
				cfg.Model = next.Model
				continue
			}
			failure := NewTextMessage("assistant", "")
			bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: failure})
			bus.Emit(Event{Type: EventMessageEnd, Turn: turn, Message: failure})
			bus.Emit(Event{Type: EventTurnEnd, Turn: turn, Message: failure, Err: err})
			transcript.completedTurns = turn
			return end(nil, err, StopReasonError)
		}
		transcript.append(assistantMsg)

		if cfg.TokenBudget > 0 {
			if transcript.totalUsage.TotalTokens >= cfg.TokenBudget {
				cfg.Logger.Warnf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget)
				result := transcript.result(messageContent(assistantMsg), turn, fmt.Errorf("token budget exhausted: %d/%d", transcript.totalUsage.TotalTokens, cfg.TokenBudget))
				return end(result, result.Err, StopReasonBudget)
			}
			if transcript.totalUsage.TotalTokens >= cfg.TokenBudget*DefaultTokenBudgetWarningPct/100 {
				bus.Emit(Event{Type: EventTokenBudgetWarning, Turn: turn})
				cfg.Logger.Warnf("token budget warning: %d/%d (80%%)", transcript.totalUsage.TotalTokens, cfg.TokenBudget)
			}
		}

		var toolResults []ChatMessage
		terminate := false
		if len(assistantMsg.ToolCalls) > 0 {
			cfg.Messages = append([]ChatMessage(nil), transcript.messages...)
			batch, err := executeToolCalls(ctx, cfg, bus, assistantMsg, turn)
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

		bus.Emit(Event{Type: EventTurnEnd, Turn: turn, Message: assistantMsg, ToolResults: toolResults, Usage: usage, ContextTokens: transcript.contextTokens})
		transcript.completedTurns = turn

		if cfg.MaxTurns > 0 && turn >= cfg.MaxTurns {
			cfg.Logger.Importantf("agent status=stopped turns=%d/%d tokens=%d", turn, cfg.MaxTurns, transcript.totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			return end(result, nil, StopReasonStopped)
		}

		if terminate {
			cfg.Logger.Importantf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.TotalTokens)
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

			cfg.Logger.Importantf("agent status=completed turns=%d tokens=%d", turn, transcript.totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			return end(result, nil, StopReasonCompleted)
		}
	}

}

type transcript struct {
	messages       []ChatMessage
	newMessages    []ChatMessage
	completedTurns int
	turnUsages     []TurnUsage
	totalUsage     Usage
	contextTokens  int
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

func (t *transcript) recordTurnUsage(turn int, usage *Usage) {
	if usage == nil {
		return
	}
	t.turnUsages = append(t.turnUsages, TurnUsage{
		Turn:             turn,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	})
	t.totalUsage.PromptTokens += usage.PromptTokens
	t.totalUsage.CompletionTokens += usage.CompletionTokens
	t.totalUsage.TotalTokens += usage.TotalTokens
	t.totalUsage.CacheReadTokens += usage.CacheReadTokens
	t.totalUsage.CacheWriteTokens += usage.CacheWriteTokens
	t.contextTokens = usage.PromptTokens
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

func executeToolCalls(ctx context.Context, cfg Config, bus emitter, assistantMsg ChatMessage, turn int) (toolBatchResult, error) {
	toolCalls := assistantMsg.ToolCalls
	slots := make([]toolCallSlot, len(toolCalls))

	// Phase 1: preflight all tool calls sequentially (emit start events, check beforeToolCall)
	for i, tc := range toolCalls {
		cfg.Logger.Infof("[turn %d] tool_call id=%s name=%s args=%q", turn, tc.ID, tc.Function.Name, truncate.Clip(tc.Function.Arguments, 200))
		bus.Emit(Event{
			Type:       EventToolExecutionStart,
			Turn:       turn,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			Arguments:  tc.Function.Arguments,
		})

		mode := commands.ExecSequential
		if tool, ok := cfg.Tools.GetTool(tc.Function.Name); ok {
			if pa, ok := tool.(commands.ParallelSafe); ok {
				mode = pa.ExecutionMode()
			}
		}

		slots[i] = toolCallSlot{tc: tc, mode: mode}
	}

	// Phase 2: execute tools — parallel-safe tools run concurrently, sequential tools run in order
	hasParallel := false
	for _, s := range slots {
		if s.mode == commands.ExecParallel {
			hasParallel = true
			break
		}
	}

	if hasParallel {
		var wg sync.WaitGroup
		for i := range slots {
			if slots[i].mode == commands.ExecParallel {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					slots[idx].startedAt = time.Now()
					slots[idx].result = runToolCall(ctx, cfg, assistantMsg, slots[idx].tc, turn)
				}(i)
			}
		}
		wg.Wait()
		if ctx.Err() != nil {
			return toolBatchResult{}, ctx.Err()
		}
		for i := range slots {
			if ctx.Err() != nil {
				return toolBatchResult{}, ctx.Err()
			}
			if slots[i].mode == commands.ExecSequential {
				slots[i].startedAt = time.Now()
				slots[i].result = runToolCall(ctx, cfg, assistantMsg, slots[i].tc, turn)
			}
		}
	} else {
		for i := range slots {
			if ctx.Err() != nil {
				return toolBatchResult{}, ctx.Err()
			}
			slots[i].startedAt = time.Now()
			slots[i].result = runToolCall(ctx, cfg, assistantMsg, slots[i].tc, turn)
		}
	}

	// Phase 3: emit results in original order
	messages := make([]ChatMessage, 0, len(slots))
	terminations := 0
	for _, s := range slots {
		bus.Emit(Event{
			Type:       EventToolExecutionEnd,
			Turn:       turn,
			ToolCallID: s.tc.ID,
			ToolName:   s.tc.Function.Name,
			Arguments:  s.tc.Function.Arguments,
			Result:     s.result.eventResult(),
			IsError:    s.result.isError,
			Err:        s.result.err,
			StartedAt:  s.startedAt,
		})
		cfg.Logger.Debugf("[turn %d] tool_result id=%s name=%s bytes=%d", turn, s.tc.ID, s.tc.Function.Name, len(s.result.result))
		toolMsg := toolResultToMessage(s.tc.ID, s.result)
		bus.Emit(Event{Type: EventMessageStart, Turn: turn, Message: toolMsg})
		bus.Emit(Event{Type: EventMessageEnd, Turn: turn, Message: toolMsg})
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

type toolCallSlot struct {
	tc        ToolCall
	mode      commands.ExecutionMode
	result    toolExecution
	startedAt time.Time
}

type toolExecution struct {
	result     string
	rawResult  string
	fullResult *commands.ToolResult
	isError    bool
	err        error
	flow       ToolFlowDecision
}

func runToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall, turn int) toolExecution {
	execution := beforeToolCall(ctx, cfg, assistantMsg, tc)
	if execution.result == "" && !execution.isError {
		toolResult, execErr := cfg.Tools.ExecuteTool(ctx, tc.Function.Name, tc.Function.Arguments)
		execution.result = toolResult.Text()
		execution.err = execErr
		execution.isError = execErr != nil || toolResult.IsError
		if execErr != nil {
			execution.result = fmt.Sprintf("error: %s", execErr.Error())
			cfg.Logger.Warnf("[turn %d] tool_error id=%s name=%s error=%q", turn, tc.ID, tc.Function.Name, execErr.Error())
		}
		if toolResult.Terminate {
			execution.flow = ToolFlowTerminate
		}
		if toolResult.HasImages() {
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
	return afterToolCall(ctx, cfg, assistantMsg, tc, execution)
}

func (e toolExecution) eventResult() string {
	if e.rawResult != "" {
		return e.rawResult
	}
	return e.result
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
	if cfg.BeforeToolCall == nil {
		return toolExecution{}
	}
	before, err := cfg.BeforeToolCall(ctx, BeforeToolCallContext{
		AssistantMessage: assistantMsg,
		ToolCall:         tc,
		SystemPrompt:     cfg.SystemPrompt,
		Messages:         cfg.Messages,
	})
	if err != nil {
		return toolExecution{result: fmt.Sprintf("error: %s", err.Error()), isError: true, err: err}
	}
	if before == nil || !before.Block {
		return toolExecution{}
	}
	result := before.Reason
	if result == "" {
		result = "tool execution was blocked"
	}
	return toolExecution{result: result, isError: true}
}

func afterToolCall(ctx context.Context, cfg Config, assistantMsg ChatMessage, tc ToolCall, execution toolExecution) toolExecution {
	if cfg.AfterToolCall == nil {
		return execution
	}
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
	if after == nil {
		return execution
	}
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
	return execution
}



func requestMessages(systemPrompt string, messages []ChatMessage, transform TransformContextFunc) []ChatMessage {
	out := sanitizeMessages(append([]ChatMessage(nil), messages...))
	if transform != nil {
		out = transform(out)
	}
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

func logAssistantAndUsage(logger telemetry.Logger, msg ChatMessage, usage *Usage) {
	if content := messageContent(msg); content != "" {
		logger.Infof("assistant output=%q", truncate.Clip(compactLogContent(content), 500))
	}
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

func compactLogContent(value string) string {
	return strings.Join(strings.Fields(value), " ")
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
