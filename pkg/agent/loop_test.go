package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
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

	var events []EventType
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event Event) {
			events = append(events, event.Type)
		}),
	})).Run(context.Background(), "use tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}

	want := []EventType{
		EventAgentStart,
		EventTurnStart,
		EventMessageStart,
		EventMessageEnd,
		EventLLMRequest,
		EventMessageStart,
		EventMessageEnd,
		EventToolExecutionStart,
		EventToolExecutionEnd,
		EventMessageStart,
		EventMessageEnd,
		EventTurnEnd,
		EventTurnStart,
		EventLLMRequest,
		EventMessageStart,
		EventMessageEnd,
		EventTurnEnd,
		EventAgentEnd,
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
		TransformContext: func(messages []ChatMessage) []ChatMessage {
			if len(messages) <= 1 {
				return messages
			}
			return messages[len(messages)-1:]
		},
	})
	if _, err := a.Run(context.Background(), "one"); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Run(context.Background(), "two"); err != nil {
		t.Fatalf("second prompt error = %v", err)
	}
	requests := llm.requestsSnapshot()
	if len(requests[1].Messages) != 1 || *requests[1].Messages[0].Content != "two" {
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
	})).Run(context.Background(), "use tool")
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
			{Delta: ChatMessageDelta{Role: "assistant"}},
			{Delta: ChatMessageDelta{Content: strPtr("hel")}},
			{Delta: ChatMessageDelta{Content: strPtr("lo")}},
			{Done: true},
		},
	}
	var updates int
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event Event) {
			if event.Type == EventMessageUpdate {
				updates++
			}
		}),
	})).Run(context.Background(), "stream")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "hello" {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if updates == 0 {
		t.Fatal("expected message_update events")
	}
}

func TestStreamingMessageUpdateCarriesUsage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []ChatCompletionStreamEvent{
			{Delta: ChatMessageDelta{Role: "assistant"}},
			{Delta: ChatMessageDelta{Content: strPtr("done")}},
			{Done: true, Usage: &Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}},
		},
	}
	var updateUsage *Usage
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event Event) {
			if event.Type == EventMessageUpdate && event.Usage != nil {
				updateUsage = event.Usage
			}
		}),
	})).Run(context.Background(), "stream")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if updateUsage == nil || updateUsage.TotalTokens != 12 {
		t.Fatalf("message_update usage = %#v, want total 12", updateUsage)
	}
}

func TestStatefulAgentTracksStreamingMessage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []ChatCompletionStreamEvent{
			{Delta: ChatMessageDelta{Role: "assistant"}},
			{Delta: ChatMessageDelta{Content: strPtr("hel")}},
			{Delta: ChatMessageDelta{Content: strPtr("lo")}},
			{Done: true},
		},
	}
	var sawUpdate bool
	a := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Bus: testBus(func(event Event) {
			if event.Type == EventMessageUpdate && messageContent(event.Message) != "" {
				sawUpdate = true
			}
		}),
	})

	result, err := a.Run(context.Background(), "stream")
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
				{Delta: ChatMessageDelta{Role: "assistant"}},
				{Delta: ChatMessageDelta{ToolCalls: []ToolCallDelta{{
					Index: 0,
					ID:    "call-1",
					Type:  "function",
					Function: FunctionCallDelta{
						Name:      "echo",
						Arguments: `{"value":`,
					},
				}}}},
				{Delta: ChatMessageDelta{ToolCalls: []ToolCallDelta{{
					Index:    0,
					Function: FunctionCallDelta{Arguments: `"x"}`},
				}}}},
				{Done: true},
			},
			{
				{Delta: ChatMessageDelta{Role: "assistant"}},
				{Delta: ChatMessageDelta{Content: strPtr("final")}},
				{Done: true},
			},
		},
	}
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
	})).Run(context.Background(), "stream tool")
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
	})).Run(context.Background(), "use tool")
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
	}).Run(context.Background(), "do something")
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
				Choices: []Choice{{Message: NewTextMessage("assistant", "done")}},
				Usage:   &Usage{PromptTokens: 700, CompletionTokens: 200, TotalTokens: 900},
			}, nil
		},
	}

	var sawWarning bool
	_, err := (NewAgent(Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
		Bus: testBus(func(event Event) {
			if event.Type == EventTokenBudgetWarning {
				sawWarning = true
			}
		}),
	})).Run(context.Background(), "hello")
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
					}}},
					Usage: &Usage{TotalTokens: 600},
				}, nil
			}
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done")}},
				Usage:   &Usage{TotalTokens: 500},
			}, nil
		},
	}
	tools.RegisterTool(&recordingTool{name: "echo", output: "ok"})

	result, err := (NewAgent(Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
	})).Run(context.Background(), "hello")
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
				Choices: []Choice{{Message: NewTextMessage("assistant", "done")}},
				Usage:   &Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			}, nil
		},
	}

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
	})).Run(context.Background(), "hello")
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
					}}},
					Usage: &Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230},
				}, nil
			}
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done")}},
				Usage:   &Usage{PromptTokens: 280, CompletionTokens: 20, TotalTokens: 300},
			}, nil
		},
	}

	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
	})).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.TurnUsages) != 2 {
		t.Fatalf("TurnUsages length = %d, want 2", len(result.TurnUsages))
	}
	if result.TurnUsages[0].Turn != 1 || result.TurnUsages[0].TotalTokens != 230 {
		t.Errorf("TurnUsages[0] = %+v, want turn=1 total=230", result.TurnUsages[0])
	}
	if result.TurnUsages[1].Turn != 2 || result.TurnUsages[1].TotalTokens != 300 {
		t.Errorf("TurnUsages[1] = %+v, want turn=2 total=300", result.TurnUsages[1])
	}
	if result.TotalUsage.TotalTokens != 530 {
		t.Errorf("TotalUsage.TotalTokens = %d, want 530", result.TotalUsage.TotalTokens)
	}
	if result.TotalUsage.PromptTokens != 480 {
		t.Errorf("TotalUsage.PromptTokens = %d, want 480", result.TotalUsage.PromptTokens)
	}
	if result.ContextTokens != 280 {
		t.Errorf("ContextTokens = %d, want 280 (last turn prompt tokens)", result.ContextTokens)
	}
}

func TestTurnEndEventCarriesUsage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			return &ChatCompletionResponse{
				Choices: []Choice{{Message: NewTextMessage("assistant", "done")}},
				Usage:   &Usage{PromptTokens: 500, CompletionTokens: 40, TotalTokens: 540},
			}, nil
		},
	}

	var turnEndUsage *Usage
	var turnEndContext int
	_, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event Event) {
			if event.Type == EventTurnEnd {
				turnEndUsage = event.Usage
				turnEndContext = event.ContextTokens
			}
		}),
	})).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if turnEndUsage == nil {
		t.Fatal("EventTurnEnd.Usage is nil")
	}
	if turnEndUsage.TotalTokens != 540 {
		t.Errorf("EventTurnEnd Usage.TotalTokens = %d, want 540", turnEndUsage.TotalTokens)
	}
	if turnEndContext != 500 {
		t.Errorf("EventTurnEnd ContextTokens = %d, want 500", turnEndContext)
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

	a.LoadMessages([]ChatMessage{
		NewTextMessage("user", "first question"),
		NewTextMessage("assistant", "first answer"),
		NewTextMessage("user", "second question"),
		NewTextMessage("assistant", ""),
	})

	result, err := a.Run(context.Background(), "continue")
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
		if msg.Role == "assistant" && messageContent(msg) == "" && len(msg.ToolCalls) == 0 {
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
	}).Run(context.Background(), "main task")
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
	}).Run(context.Background(), "task")
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
	}).Run(context.Background(), "scan things")
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
	}).Run(context.Background(), "start background scan")
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

	time.Sleep(500 * time.Millisecond)

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
	}).Run(context.Background(), "run a scan")
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
		if m.Content != nil && strings.Contains(*m.Content, "session_completion") {
			found = true
			if !strings.Contains(*m.Content, "background-result") {
				t.Errorf("session completion should contain stdout, got: %s", *m.Content)
			}
			break
		}
	}
	if !found {
		var contents []string
		for _, m := range turn2Msgs {
			if m.Content != nil {
				contents = append(contents, *m.Content)
			}
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
	time.Sleep(500 * time.Millisecond)

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

	cms := msg.ToChatMessages()
	if len(cms) != 1 {
		t.Fatalf("expected 1 chat message, got %d", len(cms))
	}
	if !strings.Contains(*cms[0].Content, "session_completion") {
		t.Errorf("chat message should contain session_completion XML, got: %s", *cms[0].Content)
	}
}

// --- Cache usage tests ---

func TestTurnUsageCacheAccumulation(t *testing.T) {
	usage1 := &Usage{
		PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
		CacheReadTokens: 0, CacheWriteTokens: 80,
	}
	usage2 := &Usage{
		PromptTokens: 150, CompletionTokens: 15, TotalTokens: 165,
		CacheReadTokens: 80, CacheWriteTokens: 0,
	}

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			{Choices: []Choice{{
				Message: ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call_1", Type: "function",
						Function: FunctionCall{Name: "read", Arguments: `{}`},
					}},
				},
			}}, Usage: usage1},
			{Choices: []Choice{{
				Message: NewTextMessage("assistant", "done"),
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
	})).Run(context.Background(), "read something")
	if err != nil {
		t.Fatal(err)
	}

	if result.TotalUsage.CacheReadTokens != 80 {
		t.Errorf("TotalUsage.CacheReadTokens = %d, want 80", result.TotalUsage.CacheReadTokens)
	}
	if result.TotalUsage.CacheWriteTokens != 80 {
		t.Errorf("TotalUsage.CacheWriteTokens = %d, want 80", result.TotalUsage.CacheWriteTokens)
	}
	if result.TotalUsage.PromptTokens != 250 {
		t.Errorf("TotalUsage.PromptTokens = %d, want 250", result.TotalUsage.PromptTokens)
	}

	if len(result.TurnUsages) != 2 {
		t.Fatalf("expected 2 TurnUsages, got %d", len(result.TurnUsages))
	}
	if result.TurnUsages[0].CacheWriteTokens != 80 {
		t.Errorf("Turn 1 CacheWriteTokens = %d, want 80", result.TurnUsages[0].CacheWriteTokens)
	}
	if result.TurnUsages[1].CacheReadTokens != 80 {
		t.Errorf("Turn 2 CacheReadTokens = %d, want 80", result.TurnUsages[1].CacheReadTokens)
	}

	t.Logf("Accumulation OK: total prompt=%d cache_read=%d cache_write=%d",
		result.TotalUsage.PromptTokens, result.TotalUsage.CacheReadTokens, result.TotalUsage.CacheWriteTokens)
}

func TestEventCarriesCacheUsage(t *testing.T) {
	usage := &Usage{
		PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110,
		CacheReadTokens: 60, CacheWriteTokens: 20,
	}

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			{Choices: []Choice{{
				Message: NewTextMessage("assistant", "hi"),
			}}, Usage: usage},
		},
	}

	var captured *Usage
	handler := func(e Event) {
		if e.Type == EventTurnEnd && e.Usage != nil {
			captured = e.Usage
		}
	}

	_, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        commands.NewRegistry(),
		Model:        "test",
		SystemPrompt: "sys",
		Bus:          testBus(func(e Event) { handler(e) }),
		Logger:       telemetry.NopLogger(),
	})).Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if captured == nil {
		t.Fatal("EventTurnEnd did not carry usage")
	}
	if captured.CacheReadTokens != 60 {
		t.Errorf("EventTurnEnd CacheReadTokens = %d, want 60", captured.CacheReadTokens)
	}
	if captured.CacheWriteTokens != 20 {
		t.Errorf("EventTurnEnd CacheWriteTokens = %d, want 20", captured.CacheWriteTokens)
	}
	fmt.Printf("Event carries cache usage: read=%d write=%d\n", captured.CacheReadTokens, captured.CacheWriteTokens)
}
