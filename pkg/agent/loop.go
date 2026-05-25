package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)


func runLoop(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent provider is nil")
	}
	if cfg.Tools == nil {
		cfg.Tools = command.NewRegistry()
	}

	transcript := newTranscript(cfg.Messages, 8)
	turn := 0

	emitFn := cfg.Emit
	ib := cfg.Inbox
	if err := emit(ctx, emitFn, Event{Type: EventAgentStart}); err != nil {
		return nil, err
	}
	ended := false
	end := func(result *Result, err error, stop StopReason) (*Result, error) {
		if result == nil {
			result = transcript.result("", transcript.completedTurns, err)
		}
		if err != nil && result.Err == nil {
			result.Err = err
		}
		if !ended {
			ended = true
			endEvent := Event{
				Type:        EventAgentEnd,
				Turn:        result.Turns,
				Messages:    append([]provider.ChatMessage(nil), result.Messages...),
				NewMessages: append([]provider.ChatMessage(nil), result.NewMessages...),
				Err:         result.Err,
				Stop:        stop,
			}
			if emitErr := emit(ctx, emitFn, endEvent); emitErr != nil && err == nil {
				err = emitErr
				result.Err = emitErr
			}
		}
		return result, err
	}

	var totalUsage provider.Usage

	for turn = 1; ; turn++ {
		if err := ctx.Err(); err != nil {
			failure := provider.NewTextMessage("assistant", "")
			transcript.append(failure)
			return end(nil, err, StopReasonCancelled)
		}
		if err := emit(ctx, emitFn, Event{Type: EventTurnStart, Turn: turn}); err != nil {
			return end(nil, err, StopReasonError)
		}

		if ib != nil {
			inboxMsgs := ib.Drain()
			for i, msg := range inboxMsgs {
				if cfg.Expander != nil {
					inboxMsgs[i] = cfg.Expander.Expand(msg)
				}
				for _, cm := range inboxMsgs[i].ToChatMessages() {
					transcript.append(cm)
					if err := emitMessage(ctx, emitFn, turn, cm); err != nil {
						return end(nil, err, StopReasonError)
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

		reqMessages := requestMessages(cfg.SystemPrompt, transcript.messages, cfg.TransformContext)
		cfg.Logger.Debugf("[turn %d] sending %d messages to LLM", turn, len(reqMessages))

		assistantMsg, usage, err := requestWithRetry(ctx, cfg, reqMessages, cfg.Tools.ToolDefinitions(), turn)
		if usage != nil {
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}
		if err != nil {
			failure := provider.NewTextMessage("assistant", "")
			transcript.append(failure)
			if emitErr := emitMessage(ctx, emitFn, turn, failure); emitErr != nil {
				return end(nil, emitErr, StopReasonError)
			}
			if emitErr := emit(ctx, emitFn, Event{Type: EventTurnEnd, Turn: turn, Message: failure, Err: err}); emitErr != nil {
				return end(nil, emitErr, StopReasonError)
			}
			transcript.completedTurns = turn
			return end(nil, err, StopReasonError)
		}
		transcript.append(assistantMsg)

		if cfg.TokenBudget > 0 {
			if totalUsage.TotalTokens >= cfg.TokenBudget {
				cfg.Logger.Warnf("token budget exhausted: %d/%d", totalUsage.TotalTokens, cfg.TokenBudget)
				result := transcript.result(messageContent(assistantMsg), turn, fmt.Errorf("token budget exhausted: %d/%d", totalUsage.TotalTokens, cfg.TokenBudget))
				result.TotalUsage = totalUsage
				return end(result, result.Err, StopReasonBudget)
			}
			if totalUsage.TotalTokens >= cfg.TokenBudget*DefaultTokenBudgetWarningPct/100 {
				_ = emit(ctx, emitFn, Event{Type: EventTokenBudgetWarning, Turn: turn})
				cfg.Logger.Warnf("token budget warning: %d/%d (80%%)", totalUsage.TotalTokens, cfg.TokenBudget)
			}
		}

		var toolResults []provider.ChatMessage
		terminate := false
		if len(assistantMsg.ToolCalls) > 0 {
			cfg.Messages = append([]provider.ChatMessage(nil), transcript.messages...)
			batch, err := executeToolCalls(ctx, cfg, assistantMsg, turn)
			if err != nil {
				return end(nil, err, StopReasonError)
			}
			toolResults = batch.messages
			terminate = batch.terminate
			transcript.append(toolResults...)
		}

		if err := emit(ctx, emitFn, Event{Type: EventTurnEnd, Turn: turn, Message: assistantMsg, ToolResults: toolResults}); err != nil {
			return end(nil, err, StopReasonError)
		}
		transcript.completedTurns = turn

		if cfg.ShouldStopAfterTurn != nil {
			messages, newMessages := transcript.snapshot()
			stop, err := cfg.ShouldStopAfterTurn(ctx, ShouldStopAfterTurnContext{
				Message:      assistantMsg,
				ToolResults:  append([]provider.ChatMessage(nil), toolResults...),
				SystemPrompt: cfg.SystemPrompt,
				Messages:     messages,
				Tools:        cfg.Tools,
				NewMessages:  newMessages,
			})
			if err != nil {
				return end(nil, err, StopReasonError)
			}
			if stop {
				cfg.Logger.Importantf("agent status=stopped turns=%d tokens=%d", turn, totalUsage.TotalTokens)
				result := transcript.result(messageContent(assistantMsg), turn, nil)
				result.TotalUsage = totalUsage
				return end(result, nil, StopReasonStopped)
			}
		}

		if terminate {
			cfg.Logger.Importantf("agent status=completed turns=%d tokens=%d", turn, totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			result.TotalUsage = totalUsage
			return end(result, nil, StopReasonTerminated)
		}
		if len(assistantMsg.ToolCalls) == 0 {
			if ib != nil && ib.Len() > 0 {
				cfg.Logger.Debugf("[turn %d] continuing for pending inbox message(s)", turn)
				continue
			}
			if ib != nil && !ib.Closed() && cfg.KeepAlive != nil && cfg.KeepAlive() {
				cfg.Logger.Debugf("[turn %d] waiting for inbox while background work is active", turn)
				pollCtx, cancel := context.WithTimeout(ctx, cfg.InboxIdlePollInterval)
				hasMessage := ib.Wait(pollCtx)
				cancel()
				if hasMessage {
					continue
				}
				if cfg.KeepAlive() {
					continue
				}
			}
			cfg.Logger.Importantf("agent status=completed turns=%d tokens=%d", turn, totalUsage.TotalTokens)
			result := transcript.result(messageContent(assistantMsg), turn, nil)
			result.TotalUsage = totalUsage
			return end(result, nil, StopReasonCompleted)
		}
	}

}

type transcript struct {
	messages       []provider.ChatMessage
	newMessages    []provider.ChatMessage
	completedTurns int
}

func newTranscript(base []provider.ChatMessage, newCapacity int) *transcript {
	return &transcript{
		messages:    append([]provider.ChatMessage(nil), base...),
		newMessages: make([]provider.ChatMessage, 0, newCapacity),
	}
}

func (t *transcript) append(messages ...provider.ChatMessage) {
	t.messages = append(t.messages, messages...)
	t.newMessages = append(t.newMessages, messages...)
}

func (t *transcript) snapshot() ([]provider.ChatMessage, []provider.ChatMessage) {
	return append([]provider.ChatMessage(nil), t.messages...), append([]provider.ChatMessage(nil), t.newMessages...)
}

func (t *transcript) result(output string, turns int, err error) *Result {
	messages, newMessages := t.snapshot()
	return &Result{
		Output:      output,
		NewMessages: newMessages,
		Messages:    messages,
		Turns:       turns,
		Err:         err,
	}
}

func emitMessage(ctx context.Context, emitFn EventHandler, turn int, msg provider.ChatMessage) error {
	if err := emit(ctx, emitFn, Event{Type: EventMessageStart, Turn: turn, Message: msg}); err != nil {
		return err
	}
	return emit(ctx, emitFn, Event{Type: EventMessageEnd, Turn: turn, Message: msg})
}

type toolBatchResult struct {
	messages  []provider.ChatMessage
	terminate bool
}

func executeToolCalls(ctx context.Context, cfg Config, assistantMsg provider.ChatMessage, turn int) (toolBatchResult, error) {
	results := make([]provider.ChatMessage, 0, len(assistantMsg.ToolCalls))
	terminations := 0
	for _, tc := range assistantMsg.ToolCalls {
		cfg.Logger.Infof("tool_call name=%s args=%q", tc.Function.Name, preview(tc.Function.Arguments, 200))

		if err := emit(ctx, cfg.Emit, Event{
			Type:       EventToolExecutionStart,
			Turn:       turn,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			Arguments:  tc.Function.Arguments,
		}); err != nil {
			return toolBatchResult{}, err
		}

		execution := runToolCall(ctx, cfg, assistantMsg, tc)

		if err := emit(ctx, cfg.Emit, Event{
			Type:       EventToolExecutionEnd,
			Turn:       turn,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			Arguments:  tc.Function.Arguments,
			Result:     execution.result,
			IsError:    execution.isError,
			Err:        execution.err,
		}); err != nil {
			return toolBatchResult{}, err
		}
		cfg.Logger.Debugf("tool_result name=%s bytes=%d", tc.Function.Name, len(execution.result))
		toolMsg := provider.NewToolResultMessage(tc.ID, execution.result)
		if err := emitMessage(ctx, cfg.Emit, turn, toolMsg); err != nil {
			return toolBatchResult{}, err
		}
		results = append(results, toolMsg)
		if execution.flow == ToolFlowTerminate {
			terminations++
		}
	}
	return toolBatchResult{
		messages:  results,
		terminate: len(results) > 0 && terminations == len(results),
	}, nil
}

type toolExecution struct {
	result  string
	isError bool
	err     error
	flow    ToolFlowDecision
}

func runToolCall(ctx context.Context, cfg Config, assistantMsg provider.ChatMessage, tc provider.ToolCall) toolExecution {
	execution := beforeToolCall(ctx, cfg, assistantMsg, tc)
	if execution.result == "" && !execution.isError {
		result, execErr := cfg.Tools.ExecuteTool(ctx, tc.Function.Name, tc.Function.Arguments,
			command.ToolContext{SystemPrompt: cfg.SystemPrompt, Messages: cfg.Messages})
		execution.result = result
		execution.err = execErr
		execution.isError = execErr != nil
		if execErr != nil {
			execution.result = fmt.Sprintf("error: %s", execErr.Error())
			cfg.Logger.Warnf("tool_error name=%s error=%q", tc.Function.Name, execErr.Error())
		}
	}
	execution.result = truncateResultSize(execution.result, cfg.MaxResultSize)
	return afterToolCall(ctx, cfg, assistantMsg, tc, execution)
}

func beforeToolCall(ctx context.Context, cfg Config, assistantMsg provider.ChatMessage, tc provider.ToolCall) toolExecution {
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

func afterToolCall(ctx context.Context, cfg Config, assistantMsg provider.ChatMessage, tc provider.ToolCall, execution toolExecution) toolExecution {
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

func truncateResult(result string) string {
	return truncateResultSize(result, DefaultMaxResultSize)
}

func truncateResultSize(result string, maxSize int) string {
	if len(result) <= maxSize {
		return result
	}
	return result[:maxSize] + fmt.Sprintf(
		"\n\n[truncated: showing %d of %d bytes. Refine your query or use filter/parse tools to access specific parts.]",
		maxSize, len(result))
}

func preview(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func requestMessages(systemPrompt string, messages []provider.ChatMessage, transform TransformContextFunc) []provider.ChatMessage {
	out := append([]provider.ChatMessage(nil), messages...)
	if transform != nil {
		out = transform(out)
	}
	if systemPrompt != "" {
		out = append([]provider.ChatMessage{provider.NewTextMessage("system", systemPrompt)}, out...)
	}
	return out
}

func emit(ctx context.Context, fn EventHandler, event Event) error {
	if fn == nil {
		return nil
	}
	return fn(ctx, event)
}

func messageContent(msg provider.ChatMessage) string {
	if msg.Content == nil {
		return ""
	}
	return *msg.Content
}

func logAssistantAndUsage(logger telemetry.Logger, msg provider.ChatMessage, usage *provider.Usage) {
	if content := messageContent(msg); content != "" {
		logger.Infof("assistant output=%q", preview(compactLogContent(content), 500))
	}
	if usage != nil {
		logger.Debugf("usage prompt=%d completion=%d total=%d",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
}

func compactLogContent(value string) string {
	return strings.Join(strings.Fields(value), " ")
}


func normalizeConfig(cfg Config) Config {
	if cfg.Logger == nil {
		cfg.Logger = telemetry.NopLogger()
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultMaxRetries
	}
	if cfg.MaxResultSize <= 0 {
		cfg.MaxResultSize = DefaultMaxResultSize
	}
	if cfg.InboxIdlePollInterval <= 0 {
		cfg.InboxIdlePollInterval = DefaultInboxIdlePollInterval
	}
	return cfg
}

type messageBuilder struct {
	role             string
	content          strings.Builder
	reasoningContent strings.Builder
	toolCalls        map[int]*provider.ToolCall
}

func newMessageBuilder() *messageBuilder {
	return &messageBuilder{
		role:      "assistant",
		toolCalls: make(map[int]*provider.ToolCall),
	}
}

func (b *messageBuilder) Apply(delta provider.ChatMessageDelta) provider.ChatMessage {
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
			tc = &provider.ToolCall{Type: "function"}
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

func (b *messageBuilder) Message() provider.ChatMessage {
	content := b.content.String()
	msg := provider.ChatMessage{Role: b.role}
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
		msg.ToolCalls = make([]provider.ToolCall, 0, len(indexes))
		for _, index := range indexes {
			msg.ToolCalls = append(msg.ToolCalls, *b.toolCalls[index])
		}
	}
	return msg
}
