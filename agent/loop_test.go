package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/agent/tmux"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestRunEmitsTurnEndAfterToolResults(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"x"}`,
					},
				}},
			}),
			chatResponse(NewTextMessage("assistant", "final")),
		},
	}

	var events []string
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event *aop.Event) {
			events = append(events, eventKind(event))
		}),
	})).Run(context.Background(), TextInput("use tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}

	want := []string{
		"message",
		"status",
		"message",
		"tool.call",
		"tool.result",
		"status",
		"message",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestTransformContextAppliesOnlyToProviderRequest(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "one")),
			chatResponse(NewTextMessage("assistant", "two")),
		},
	}
	a := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		TransformContext: func(messages []*aop.Message) []*aop.Message {
			if len(messages) <= 1 {
				return messages
			}
			return messages[len(messages)-1:]
		},
	})
	if _, err := a.Run(context.Background(), TextInput("one")); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Run(context.Background(), TextInput("two")); err != nil {
		t.Fatalf("second prompt error = %v", err)
	}
	requests := llm.requestsSnapshot()
	if len(requests[1].Messages) != 1 || provider.MessageText(requests[1].Messages[0]) != "two" {
		t.Fatalf("transform not applied to request: %#v", requests[1].Messages)
	}
	if got := len(a.state.Messages); got != 4 {
		t.Fatalf("agent state messages = %d, want 4", got)
	}
}

func TestMaxTurnsStopsBeforeNextModelCall(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"x"}`,
					},
				}},
			}),
			chatResponse(NewTextMessage("assistant", "should not be called")),
		},
	}

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		MaxTurns: 1,
	})).Run(context.Background(), TextInput("use tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Turns != 1 {
		t.Fatalf("turns = %d, want 1", result.Turns)
	}
	if got := len(llm.requestsSnapshot()); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

func TestStreamingProviderEmitsMessageUpdates(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []ChatCompletionStreamEvent{
			roleDelta("assistant"),
			textDelta("hel"),
			textDelta("lo"),
			{Done: true},
		},
	}
	var updates int
	var contentDeltas []string
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event *aop.Event) {
			if eventKind(event) != "message.delta" {
				return
			}
			data := event.GetMessageDelta()
			if data == nil {
				return
			}
			updates++
			if _, ok := data.Value.(*aop.MessageDelta_Text); ok {
				contentDeltas = append(contentDeltas, data.GetText())
			}
		}),
	})).Run(context.Background(), TextInput("stream"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "hello" {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if updates == 0 {
		t.Fatal("expected message_update events")
	}
	if got := strings.Join(contentDeltas, ""); got != "hello" {
		t.Fatalf("content deltas = %q, want hello", got)
	}
}

func TestStreamingMessageUpdateCarriesUsage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []ChatCompletionStreamEvent{
			roleDelta("assistant"),
			textDelta("done"),
			{Done: true, Usage: provider.TokenUsage(10, 2, 12, 0, 0)},
		},
	}
	var updateUsage *aop.TokenUsage
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event *aop.Event) {
			if eventKind(event) != "usage" {
				return
			}
			data := event.GetUsage()
			if data == nil {
				return
			}
			updateUsage = data
		}),
	})).Run(context.Background(), TextInput("stream"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if updateUsage == nil || updateUsage.TotalTokens != 12 {
		t.Fatalf("usage event = %#v, want total 12", updateUsage)
	}
}

func TestStatefulAgentTracksStreamingMessage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []ChatCompletionStreamEvent{
			roleDelta("assistant"),
			textDelta("hel"),
			textDelta("lo"),
			{Done: true},
		},
	}
	var sawUpdate bool
	a := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event *aop.Event) {
			if eventKind(event) == "message.delta" {
				sawUpdate = true
			}
		}),
	})

	result, err := a.Run(context.Background(), TextInput("stream"))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if result.Output != "hello" {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if !sawUpdate {
		t.Fatal("no message_update event during streaming")
	}
}

func TestStreamingToolCallDeltasAreAggregated(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "ok"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{
		streamEventBatches: [][]ChatCompletionStreamEvent{
			{
				roleDelta("assistant"),
				toolCallDelta(0, "call-1", "echo", `{"value":`),
				toolCallDelta(0, "", "", `"x"}`),
				{Done: true},
			},
			{
				roleDelta("assistant"),
				textDelta("final"),
				{Done: true},
			},
		},
	}
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
	})).Run(context.Background(), TextInput("stream tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final" {
		t.Fatalf("result = %q, want final", result.Output)
	}
	if got := echo.callsSnapshot(); !reflect.DeepEqual(got, []string{`{"value":"x"}`}) {
		t.Fatalf("tool calls = %#v", got)
	}
}

func TestOutputLimitToolCallIsRejectedAndRetried(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "must not run"}
	tools.RegisterTool(echo)
	beforeCalled := false
	afterCalled := false
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		{Choices: []Choice{{
			Message: ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID: "call-truncated", Type: "function",
					Function: FunctionCall{Name: "echo", Arguments: `{"value":"cut off`},
				}},
			}.toAOP(),
			FinishReason: "max_tokens",
		}}},
		chatResponse(NewTextMessage("assistant", "recovered")),
	}}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		BeforeToolCall: func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error) {
			beforeCalled = true
			return nil, nil
		},
		AfterToolCall: func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error) {
			afterCalled = true
			return nil, nil
		},
	}).Run(context.Background(), TextInput("use a tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("output = %q, want recovered", result.Output)
	}
	if calls := echo.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("truncated tool was executed: %#v", calls)
	}
	if beforeCalled || afterCalled {
		t.Fatalf("tool hooks ran for rejected call: before=%v after=%v", beforeCalled, afterCalled)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	var truncated *aop.Message
	var errorResult *aop.ToolResult
	for _, msg := range requests[1].Messages {
		if msg.Role == "assistant" && len(provider.MessageToolCalls(msg)) > 0 {
			truncated = msg
		}
		if msg.Role == "tool" {
			if r := provider.MessageToolResult(msg); r != nil && r.CallId == "call-truncated" {
				errorResult = r
			}
		}
	}
	if truncated == nil {
		t.Fatal("assistant message with tool call not found")
	}
	truncatedCalls := provider.MessageToolCalls(truncated)
	if got := string(truncatedCalls[0].GetArguments().GetData()); got != "{}" {
		t.Fatalf("sanitized arguments = %q, want {}", got)
	}
	if errorResult == nil || !errorResult.IsError || !strings.Contains(tool.ResultText(errorResult), "Retry") {
		t.Fatalf("error tool result = %#v", errorResult)
	}
}

func TestStreamingOutputLimitToolCallPreservesFinishReason(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "must not run"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{streamEventBatches: [][]ChatCompletionStreamEvent{
		{
			roleDelta("assistant"),
			toolCallDelta(0, "stream-truncated", "echo", `{"value":"partial`),
			{FinishReason: "length"},
			{Done: true},
		},
		{
			roleDelta("assistant"),
			textDelta("recovered"),
			{FinishReason: "stop"},
			{Done: true},
		},
	}}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
	}).Run(context.Background(), TextInput("use a streaming tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("output = %q, want recovered", result.Output)
	}
	if calls := echo.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("truncated tool was executed: %#v", calls)
	}
	// A "length" finish reason marks the streamed tool call truncated: the call
	// is rejected and its arguments are sanitized to "{}" in the transcript.
	var sanitized string
	for _, msg := range result.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range provider.MessageToolCalls(msg) {
			if call.Id == "stream-truncated" {
				sanitized = string(call.GetArguments().GetData())
			}
		}
	}
	if sanitized != "{}" {
		t.Fatalf("truncated stream tool call arguments = %q, want {}", sanitized)
	}
}

func TestStreamingMalformedToolCallIsRejectedAfterNormalTerminalMarker(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "must not run"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{streamEventBatches: [][]ChatCompletionStreamEvent{
		{
			roleDelta("assistant"),
			toolCallDelta(0, "stream-malformed", "echo", `{"value":"partial`),
			{FinishReason: "tool_calls"},
			{Done: true},
		},
		{
			roleDelta("assistant"),
			textDelta("recovered"),
			{FinishReason: "stop"},
			{Done: true},
		},
	}}

	result, err := NewAgent(Config{
		Provider: llm, Tools: tools, Model: "test", Stream: true,
	}).Run(context.Background(), TextInput("use a streaming tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("output = %q, want recovered", result.Output)
	}
	if calls := echo.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("malformed tool was executed: %#v", calls)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	var rejectedCall *aop.Message
	var errorResult *aop.ToolResult
	for _, message := range requests[1].Messages {
		if message.Role == "assistant" && len(provider.MessageToolCalls(message)) > 0 {
			rejectedCall = message
		}
		if message.Role == "tool" {
			if r := provider.MessageToolResult(message); r != nil && r.CallId == "stream-malformed" {
				errorResult = r
			}
		}
	}
	if rejectedCall == nil {
		t.Fatal("assistant message with rejected tool call not found")
	}
	rejectedCalls := provider.MessageToolCalls(rejectedCall)
	if len(rejectedCalls) != 1 || string(rejectedCalls[0].GetArguments().GetData()) != "{}" {
		t.Fatalf("rejected tool call = %#v", rejectedCalls)
	}
	if errorResult == nil || !errorResult.IsError || !strings.Contains(tool.ResultText(errorResult), "invalid") {
		t.Fatalf("error tool result = %#v", errorResult)
	}
}

func TestToolHooksCanBlockRewriteAndTerminate(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "raw"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"blocked"}`,
					},
				}},
			}),
		},
	}
	rewritten := "rewritten result"
	isError := false

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		BeforeToolCall: func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error) {
			return &BeforeToolCallResult{Block: true, Reason: "blocked by test"}, nil
		},
		AfterToolCall: func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error) {
			return &AfterToolCallResult{Result: &rewritten, IsError: &isError, Flow: ToolFlowTerminate}, nil
		},
	})).Run(context.Background(), TextInput("use tool"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := echo.callsSnapshot(); len(got) != 0 {
		t.Fatalf("tool calls = %#v, want blocked", got)
	}
	if len(llm.requestsSnapshot()) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(llm.requestsSnapshot()))
	}
	if !hasToolMessage(result.Messages, "call-1", rewritten) {
		t.Fatalf("result messages missing rewritten tool result: %#v", result.Messages)
	}
}

func TestFinishToolTerminatesLoop(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(NewFinishTool())

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID: "call_1", Type: "function",
					Function: FunctionCall{Name: "finish", Arguments: `{"summary":"all done"}`},
				}},
			}),
		},
	}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus:      testBus(nil),
	}).Run(context.Background(), TextInput("do something"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stop != StopReasonTerminated {
		t.Fatalf("stop = %q, want %q", result.Stop, StopReasonTerminated)
	}
}

func TestTokenBudgetWarning(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done").toAOP()}},
				Usage:   provider.TokenUsage(700, 200, 900, 0, 0),
			}, nil
		},
	}

	var sawWarning bool
	_, err := (NewAgent(Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
		Bus: testBus(func(event *aop.Event) {
			if eventKind(event) != "status" {
				return
			}
			data := event.GetStatus()
			if data != nil && data.State == statusTokenBudgetWarning {
				sawWarning = true
			}
		}),
	})).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sawWarning {
		t.Fatal("expected token_budget_warning event at 90% usage")
	}
}

func TestTokenBudgetExceeded(t *testing.T) {
	tools := commands.NewRegistry()
	turn := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			turn++
			if turn == 1 {
				return &ChatCompletionResponse{
					Choices: []Choice{{Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{{
							ID:       "call-1",
							Type:     "function",
							Function: FunctionCall{Name: "echo", Arguments: `{}`},
						}},
					}.toAOP()}},
					Usage: provider.TokenUsage(0, 0, 600, 0, 0),
				}, nil
			}
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done").toAOP()}},
				Usage:   provider.TokenUsage(0, 0, 500, 0, 0),
			}, nil
		},
	}
	tools.RegisterTool(&recordingTool{name: "echo", output: "ok"})

	result, err := (NewAgent(Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
	})).Run(context.Background(), TextInput("hello"))
	if err == nil {
		t.Fatal("Run() error = nil, want budget exceeded error")
	}
	if !strings.Contains(err.Error(), "token budget exhausted") {
		t.Fatalf("error = %v, want token budget exhausted", err)
	}
	if result == nil || result.TotalUsage.TotalTokens == 0 {
		t.Fatal("result should contain accumulated usage")
	}
}

func TestBudgetExhaustionDoesNotKeepUnpairedToolCall(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "must not run"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{{
		Choices: []Choice{{Message: ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{ID: "cut-off", Type: "function", Function: FunctionCall{
				Name: "echo", Arguments: `{"value":"partial`},
			}},
		}.toAOP(), FinishReason: "max_tokens"}},
		Usage: provider.TokenUsage(0, 0, 1000, 0, 0),
	}}}

	result, err := NewAgent(Config{
		Provider: llm, Tools: tools, Model: "test", TokenBudget: 1000,
	}).Run(context.Background(), TextInput("use a tool"))
	if err == nil || result == nil || result.Stop != StopReasonBudget {
		t.Fatalf("Run() result=%#v error=%v, want budget stop", result, err)
	}
	if len(result.Messages) != 1 || len(echo.callsSnapshot()) != 0 {
		t.Fatalf("budget stop kept or executed tool call: messages=%#v calls=%#v", result.Messages, echo.callsSnapshot())
	}
}

func TestTruncateResultIncludesSize(t *testing.T) {
	large := strings.Repeat("x\n", DefaultMaxResultSize)
	tr := truncate.Head(large, truncate.Options{MaxBytes: DefaultMaxResultSize})
	if !tr.Truncated {
		t.Fatal("expected truncation")
	}
	msg := fmt.Sprintf("%d/%d lines", tr.OutputLines, tr.TotalLines)
	if tr.OutputLines >= tr.TotalLines {
		t.Fatalf("expected output lines < total lines, got %d/%d", tr.OutputLines, tr.TotalLines)
	}
	_ = msg
}

func TestResultIncludesTotalUsage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done").toAOP()}},
				Usage:   provider.TokenUsage(100, 50, 150, 0, 0),
			}, nil
		},
	}

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
	})).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.TotalUsage.TotalTokens != 150 {
		t.Fatalf("TotalUsage.TotalTokens = %d, want 150", result.TotalUsage.TotalTokens)
	}
}

func TestResultIncludesPerTurnUsageAndContextTokens(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "ok"})

	turn := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			turn++
			if turn == 1 {
				return &ChatCompletionResponse{
					Choices: []Choice{{Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{{
							ID: "call-1", Type: "function",
							Function: FunctionCall{Name: "echo", Arguments: `{}`},
						}},
					}.toAOP()}},
					Usage: provider.TokenUsage(200, 30, 230, 0, 0),
				}, nil
			}
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done").toAOP()}},
				Usage:   provider.TokenUsage(280, 20, 300, 0, 0),
			}, nil
		},
	}

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
	})).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.TurnUsages) != 2 {
		t.Fatalf("TurnUsages length = %d, want 2", len(result.TurnUsages))
	}
	if result.TurnUsages[0].TotalTokens != 230 {
		t.Errorf("TurnUsages[0] = %+v, want total=230", result.TurnUsages[0])
	}
	if result.TurnUsages[1].TotalTokens != 300 {
		t.Errorf("TurnUsages[1] = %+v, want total=300", result.TurnUsages[1])
	}
	if result.TotalUsage.TotalTokens != 530 {
		t.Errorf("TotalUsage.TotalTokens = %d, want 530", result.TotalUsage.TotalTokens)
	}
	if result.TotalUsage.InputTokens != 480 {
		t.Errorf("TotalUsage.InputTokens = %d, want 480", result.TotalUsage.InputTokens)
	}
	if result.ContextTokens != 300 {
		t.Errorf("ContextTokens = %d, want 300 (last turn input + output)", result.ContextTokens)
	}
}

func TestTurnEndEventCarriesUsage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done").toAOP()}},
				Usage:   provider.TokenUsage(500, 40, 540, 0, 0),
			}, nil
		},
	}

	var turnEndUsage *aop.TokenUsage
	_, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event *aop.Event) {
			switch eventKind(event) {
			case "usage":
				if data := event.GetUsage(); data != nil {
					turnEndUsage = data
				}
			}
		}),
	})).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if turnEndUsage == nil {
		t.Fatal("usage event missing")
	}
	if turnEndUsage.TotalTokens != 540 {
		t.Errorf("usage TotalTokens = %d, want 540", turnEndUsage.TotalTokens)
	}
}

func TestSanitizeMessagesFiltersStaleEmptyAssistant(t *testing.T) {
	var captured []*ChatCompletionRequest
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			captured = append(captured, cloneRequest(req))
			return chatResponse(NewTextMessage("assistant", "ok")), nil
		},
	}

	a := NewAgent(Config{
		Provider:   llm,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	a.LoadMessages([]*aop.Message{
		textMessage("user", "first question"),
		textMessage("assistant", "first answer"),
		textMessage("user", "second question"),
		textMessage("assistant", ""),
	})

	result, err := a.Run(context.Background(), TextInput("continue"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "ok" {
		t.Fatalf("output = %q, want 'ok'", result.Output)
	}
	if len(captured) == 0 {
		t.Fatal("no requests captured")
	}
	for _, msg := range captured[0].Messages {
		if msg.Role == "assistant" && messageContent(msg) == "" && len(provider.MessageToolCalls(msg)) == 0 {
			t.Error("empty assistant message was NOT filtered from LLM request")
		}
	}
}

// --- Inbox integration tests ---

func TestInboxDrainedBeforeFirstTurnLLMCall(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "ack")),
		},
	}
	ib := inbox.NewBuffered(4)
	ib.Push(inbox.NewMessage(inbox.OriginPeer, "user", "[peer] hello"))
	ib.Push(inbox.NewMessage(inbox.OriginPeer, "user", "[peer] status?"))

	result, err := NewAgent(Config{
		Provider:     llm,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
		Inbox:        ib,
	}).Run(context.Background(), TextInput("main task"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "ack" {
		t.Fatalf("result = %q, want ack", result.Output)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	msgs := requests[0].Messages
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4 (system + 2 peer + task): %#v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("msg[0].Role = %q, want system", msgs[0].Role)
	}
	if got := contentOf(msgs[1]); !strings.Contains(got, "[peer] hello") {
		t.Fatalf("msg[1] missing peer content: %q", got)
	}
	if got := contentOf(msgs[2]); !strings.Contains(got, "[peer] status?") {
		t.Fatalf("msg[2] missing peer content: %q", got)
	}
	if got := contentOf(msgs[3]); got != "main task" {
		t.Fatalf("msg[3] = %q, want main task", got)
	}
}

func TestInboxClosedDoesNotBlock(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "done")),
		},
	}

	result, err := NewAgent(Config{
		Provider:     llm,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
	}).Run(context.Background(), TextInput("task"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("result = %q, want done", result.Output)
	}
}

func TestInboxDrainedBetweenTurns(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})

	scripted := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: FunctionCall{Name: "echo", Arguments: "{}"},
				}},
			}),
			chatResponse(NewTextMessage("assistant", "final")),
		},
	}

	ib := inbox.NewBuffered(4)
	pushing := &pushingProvider{
		inner: scripted,
		inbox: ib,
		push:  inbox.NewMessage(inbox.OriginPeer, "user", "[peer] watch out for example.com"),
	}

	result, err := NewAgent(Config{
		Provider:     pushing,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
		Inbox:        ib,
	}).Run(context.Background(), TextInput("scan things"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final" {
		t.Fatalf("result = %q, want final", result.Output)
	}

	requests := scripted.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}

	turn1Msgs := requests[0].Messages
	for _, m := range turn1Msgs {
		if strings.Contains(contentOf(m), "[peer] watch out for example.com") {
			t.Fatalf("turn 1 unexpectedly contains peer message: %#v", turn1Msgs)
		}
	}

	turn2Msgs := requests[1].Messages
	found := false
	for _, m := range turn2Msgs {
		if strings.Contains(contentOf(m), "[peer] watch out for example.com") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("turn 2 missing peer message: %#v", turn2Msgs)
	}
}

func TestRunWaitsWhenKeepAliveIsTrue(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "waiting")),
			chatResponse(NewTextMessage("assistant", "final")),
		},
	}
	ib := inbox.NewBuffered(4)
	producer := ib.RegisterProducer("test-bg-task")

	go func() {
		defer producer.Done()
		time.Sleep(20 * time.Millisecond)
		ib.Push(inbox.NewMessage(inbox.OriginSession, "user", "<session_completion>scan done</session_completion>"))
	}()

	result, err := NewAgent(Config{
		Provider:     llm,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
		Inbox:        ib,
	}).Run(context.Background(), TextInput("start background scan"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final" {
		t.Fatalf("result = %q, want final", result.Output)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	found := false
	for _, msg := range requests[1].Messages {
		if strings.Contains(contentOf(msg), "<session_completion>") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("second request missing task completion: %#v", requests[1].Messages)
	}
}

// --- Session completion tests ---

func TestSessionCompletionInjectedIntoAgentLoop(t *testing.T) {
	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})

	ib := inbox.NewBuffered(8)
	sessMgr := tmux.NewManager()
	sessMgr.SetOnDone(func(info tmux.Info) {
		tail := sessMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{"session_id": info.ID}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := sessMgr.Create(dir, "echo background-result", "bg-scan", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if !ib.Wait(waitCtx) {
		t.Fatal("timed out waiting for session completion")
	}

	scripted := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: FunctionCall{Name: "echo", Arguments: "{}"},
				}},
			}),
			chatResponse(NewTextMessage("assistant", "saw the background session")),
		},
	}

	result, err := NewAgent(Config{
		Provider:     scripted,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
		Inbox:        ib,
	}).Run(context.Background(), TextInput("run a scan"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "saw the background session" {
		t.Fatalf("result = %q, want 'saw the background session'", result.Output)
	}

	requests := scripted.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("expected 2 LLM requests, got %d", len(requests))
	}

	turn2Msgs := requests[1].Messages
	found := false
	for _, m := range turn2Msgs {
		if text := provider.MessageText(m); strings.Contains(text, "session_completion") {
			found = true
			if !strings.Contains(text, "background-result") {
				t.Errorf("session completion should contain stdout, got: %s", text)
			}
			break
		}
	}
	if !found {
		var contents []string
		for _, m := range turn2Msgs {
			contents = append(contents, provider.MessageText(m))
		}
		t.Fatalf("turn 2 missing session_completion message.\nMessages:\n%s", strings.Join(contents, "\n---\n"))
	}
}

func TestSessionCompletionMetadata(t *testing.T) {
	ib := inbox.NewBuffered(4)
	sessMgr := tmux.NewManager()
	sessMgr.SetOnDone(func(info tmux.Info) {
		tail := sessMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{
			"session_id":   info.ID,
			"session_name": info.Name,
			"exit_code":    info.ExitCode,
		}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := sessMgr.Create(dir, "echo done", "test-session", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if !ib.Wait(waitCtx) {
		t.Fatal("timed out waiting for session completion")
	}

	received := ib.Drain()
	if len(received) == 0 {
		t.Fatal("expected at least 1 inbox message from session completion")
	}

	msg := received[0]
	if msg.Origin != inbox.OriginSession {
		t.Errorf("origin = %q, want %q", msg.Origin, inbox.OriginSession)
	}
	if msg.Meta["session_name"] != "test-session" {
		t.Errorf("session_name = %v, want test-session", msg.Meta["session_name"])
	}
	if msg.Meta["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", msg.Meta["exit_code"])
	}

	cms := msg.ToMessages()
	if len(cms) != 1 {
		t.Fatalf("expected 1 chat message, got %d", len(cms))
	}
	if !strings.Contains(provider.MessageText(cms[0]), "session_completion") {
		t.Errorf("chat message should contain session_completion XML, got: %s", provider.MessageText(cms[0]))
	}
}

// --- Cache usage tests ---

func TestTurnUsageCacheAccumulation(t *testing.T) {
	usage1 := provider.TokenUsage(100, 20, 120, 0, 80)
	usage2 := provider.TokenUsage(150, 15, 165, 80, 0)

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			{Choices: []Choice{{
				Message: ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call_1", Type: "function",
						Function: FunctionCall{Name: "read", Arguments: `{}`},
					}},
				}.toAOP(),
			}}, Usage: usage1},
			{Choices: []Choice{{
				Message: NewTextMessage("assistant", "done").toAOP(),
			}}, Usage: usage2},
		},
	}

	tools := commands.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "read", output: "file content"})

	result, err := (NewAgent(Config{
		Provider:       llm,
		Tools:          tools,
		Model:          "test",
		SystemPrompt:   "sys",
		CacheRetention: CacheShort,
		Logger:         telemetry.NopLogger(),
	})).Run(context.Background(), TextInput("read something"))
	if err != nil {
		t.Fatal(err)
	}

	if result.TotalUsage.Detail["cache_read"] != 80 {
		t.Errorf("TotalUsage cache_read = %d, want 80", result.TotalUsage.Detail["cache_read"])
	}
	if result.TotalUsage.Detail["cache_write"] != 80 {
		t.Errorf("TotalUsage cache_write = %d, want 80", result.TotalUsage.Detail["cache_write"])
	}
	if result.TotalUsage.InputTokens != 250 {
		t.Errorf("TotalUsage.InputTokens = %d, want 250", result.TotalUsage.InputTokens)
	}

	if len(result.TurnUsages) != 2 {
		t.Fatalf("expected 2 TurnUsages, got %d", len(result.TurnUsages))
	}
	if result.TurnUsages[0].Detail["cache_write"] != 80 {
		t.Errorf("Turn 1 cache_write = %d, want 80", result.TurnUsages[0].Detail["cache_write"])
	}
	if result.TurnUsages[1].Detail["cache_read"] != 80 {
		t.Errorf("Turn 2 cache_read = %d, want 80", result.TurnUsages[1].Detail["cache_read"])
	}

	t.Logf("Accumulation OK: total prompt=%d cache_read=%d cache_write=%d",
		result.TotalUsage.InputTokens, result.TotalUsage.Detail["cache_read"], result.TotalUsage.Detail["cache_write"])
}

func TestEventCarriesCacheUsage(t *testing.T) {
	usage := provider.TokenUsage(100, 10, 110, 60, 20)

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			{Choices: []Choice{{
				Message: NewTextMessage("assistant", "hi").toAOP(),
			}}, Usage: usage},
		},
	}

	var captured *aop.TokenUsage
	handler := func(e *aop.Event) {
		if eventKind(e) != "usage" {
			return
		}
		if data := e.GetUsage(); data != nil {
			captured = data
		}
	}

	_, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        commands.NewRegistry(),
		Model:        "test",
		SystemPrompt: "sys",
		Bus:          testBus(func(e *aop.Event) { handler(e) }),
		Logger:       telemetry.NopLogger(),
	})).Run(context.Background(), TextInput("test"))
	if err != nil {
		t.Fatal(err)
	}

	if captured == nil {
		t.Fatal("usage event missing")
	}
	if captured.Detail["cache_read"] != 60 {
		t.Errorf("usage cache_read = %d, want 60", captured.Detail["cache_read"])
	}
	if captured.Detail["cache_write"] != 20 {
		t.Errorf("usage cache_write = %d, want 20", captured.Detail["cache_write"])
	}
	fmt.Printf("Event carries cache usage: read=%d write=%d\n", captured.Detail["cache_read"], captured.Detail["cache_write"])
}
