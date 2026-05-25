package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
)

func TestRunWithoutToolsReturnsFinalText(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "done")),
		},
	}

	result, err := (Config{
		Provider:     llm,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
	}).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("result = %q, want done", result.Output)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].Messages[0].Role != "system" || *requests[0].Messages[0].Content != "system" {
		t.Fatalf("system message not injected: %#v", requests[0].Messages)
	}
}

func TestBuildSystemPromptIncludesSkills(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "read", output: "unused"})
	loaded, diagnostics := skills.LoadEmbedded()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	prompt := BuildSystemPrompt(&PromptConfig{
		Tools:  tools,
		Skills: loaded,
	})
	for _, want := range []string{
		"## Available Skills",
		"<available_skills>",
		"<name>aiscan</name>",
		"aiscan://skills/aiscan/SKILL.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, internal := range []string{"scan", "gogo", "spray", "katana", "fuzz", "zombie", "neutron"} {
		if strings.Contains(prompt, "<name>"+internal+"</name>") {
			t.Fatalf("prompt includes internal skill %q:\n%s", internal, prompt)
		}
	}
}

func TestBuildSystemPromptAllowsNilConfig(t *testing.T) {
	prompt := BuildSystemPrompt(nil)
	if !strings.Contains(prompt, "## Available Tools") {
		t.Fatalf("prompt missing tools section:\n%s", prompt)
	}
}

func TestRunExecutesToolLoop(t *testing.T) {
	tools := command.NewRegistry()
	echo := &recordingTool{name: "echo", output: "tool output"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: provider.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"x"}`,
					},
				}},
			}),
			chatResponse(provider.NewTextMessage("assistant", "final")),
		},
	}

	var events []EventType
	emit := func(_ context.Context, event Event) error {
		events = append(events, event.Type)
		return nil
	}
	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Emit:     emit,
	}).Run(context.Background(), "use tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final" {
		t.Fatalf("output = %q, want final", result.Output)
	}
	if got := echo.callsSnapshot(); !reflect.DeepEqual(got, []string{`{"value":"x"}`}) {
		t.Fatalf("tool calls = %#v", got)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if !hasToolMessage(requests[1].Messages, "call-1", "tool output") {
		t.Fatalf("second request missing tool result: %#v", requests[1].Messages)
	}
	if !containsEvent(events, EventToolExecutionStart) || !containsEvent(events, EventToolExecutionEnd) {
		t.Fatalf("tool events missing: %#v", events)
	}
}

func TestRunEmitsTurnEndAfterToolResults(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: provider.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"x"}`,
					},
				}},
			}),
			chatResponse(provider.NewTextMessage("assistant", "final")),
		},
	}

	var events []EventType
	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Emit: func(_ context.Context, event Event) error {
			events = append(events, event.Type)
			return nil
		},
	}).Run(context.Background(), "use tool")
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
		EventMessageStart,
		EventMessageEnd,
		EventToolExecutionStart,
		EventToolExecutionEnd,
		EventMessageStart,
		EventMessageEnd,
		EventTurnEnd,
		EventTurnStart,
		EventMessageStart,
		EventMessageEnd,
		EventTurnEnd,
		EventAgentEnd,
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestContinueRequiresNonAssistantLastMessage(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{}
	a := New(llm, tools, Config{Model: "test"})

	if _, err := a.Continue(context.Background()); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("Continue() error = %v, want no messages", err)
	}

	a.state.Messages = []provider.ChatMessage{provider.NewTextMessage("assistant", "done")}
	if _, err := a.Continue(context.Background()); err == nil || !strings.Contains(err.Error(), "assistant") {
		t.Fatalf("Continue() error = %v, want assistant", err)
	}
}

func TestAgentReusesConversationAcrossPrompts(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "first")),
			chatResponse(provider.NewTextMessage("assistant", "second")),
		},
	}
	a := New(llm, tools, Config{Model: "test"})
	if _, err := a.Prompt(context.Background(), "one"); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Prompt(context.Background(), "two"); err != nil {
		t.Fatalf("second prompt error = %v", err)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3: %#v", len(requests[1].Messages), requests[1].Messages)
	}
	if *requests[1].Messages[0].Content != "one" || *requests[1].Messages[1].Content != "first" || *requests[1].Messages[2].Content != "two" {
		t.Fatalf("unexpected reused context: %#v", requests[1].Messages)
	}
}

func TestAgentPromptReturnsRunScopedNewMessages(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "next")),
		},
	}
	ag := New(llm, tools, Config{Model: "test"})
	ag.state.Messages = []provider.ChatMessage{provider.NewTextMessage("user", "base")}
	result, err := ag.Prompt(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if len(result.NewMessages) != 2 {
		t.Fatalf("new messages = %d, want 2: %#v", len(result.NewMessages), result.NewMessages)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(result.Messages), result.Messages)
	}
}

func TestTransformContextAppliesOnlyToProviderRequest(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "one")),
			chatResponse(provider.NewTextMessage("assistant", "two")),
		},
	}
	a := New(llm, tools, Config{
		Model: "test",
		TransformContext: func(messages []provider.ChatMessage) []provider.ChatMessage {
			if len(messages) <= 1 {
				return messages
			}
			return messages[len(messages)-1:]
		},
	})
	if _, err := a.Prompt(context.Background(), "one"); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Prompt(context.Background(), "two"); err != nil {
		t.Fatalf("second prompt error = %v", err)
	}
	requests := llm.requestsSnapshot()
	if len(requests[1].Messages) != 1 || *requests[1].Messages[0].Content != "two" {
		t.Fatalf("transform not applied to request: %#v", requests[1].Messages)
	}
	if got := len(a.State().Messages); got != 4 {
		t.Fatalf("agent state messages = %d, want 4", got)
	}
}

func TestProviderErrorEmitsAgentEndAndUpdatesState(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{err: fmt.Errorf("boom")}
	a := New(llm, tools, Config{Model: "test"})

	var events []Event
	a.Subscribe(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})

	result, err := a.Prompt(context.Background(), "hello")
	if err == nil {
		t.Fatal("Prompt() error = nil, want error")
	}
	if result == nil || result.Err == nil {
		t.Fatalf("result = %#v, want result with Err", result)
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, []EventType{
		EventAgentStart,
		EventTurnStart,
		EventMessageStart,
		EventMessageEnd,
		EventMessageStart,
		EventMessageEnd,
		EventTurnEnd,
		EventAgentEnd,
	}) {
		t.Fatalf("events = %#v", got)
	}
	if result.Turns != 1 {
		t.Fatalf("turns = %d, want 1", result.Turns)
	}
	if len(events) == 0 || events[len(events)-1].Type != EventAgentEnd || events[len(events)-1].Err == nil {
		t.Fatalf("last event = %#v, want agent_end with error", lastEvent(events))
	}
	state := a.State()
	if state.IsRunning {
		t.Fatal("state.IsRunning = true, want false")
	}
	if !strings.Contains(state.ErrorMessage, "boom") {
		t.Fatalf("state.ErrorMessage = %q, want boom", state.ErrorMessage)
	}
}

func TestShouldStopAfterTurnStopsBeforeNextModelCall(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: provider.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"x"}`,
					},
				}},
			}),
			chatResponse(provider.NewTextMessage("assistant", "should not be called")),
		},
	}

	var sawToolResults bool
	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		ShouldStopAfterTurn: func(_ context.Context, ctx ShouldStopAfterTurnContext) (bool, error) {
			sawToolResults = len(ctx.ToolResults) == 1 && hasToolMessage(ctx.Messages, "call-1", "tool output")
			return true, nil
		},
	}).Run(context.Background(), "use tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sawToolResults {
		t.Fatal("shouldStopAfterTurn did not receive completed turn context")
	}
	if result.Turns != 1 {
		t.Fatalf("turns = %d, want 1", result.Turns)
	}
	if got := len(llm.requestsSnapshot()); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

func TestStreamingProviderEmitsMessageUpdates(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []provider.ChatCompletionStreamEvent{
			{Delta: provider.ChatMessageDelta{Role: "assistant"}},
			{Delta: provider.ChatMessageDelta{Content: strPtr("hel")}},
			{Delta: provider.ChatMessageDelta{Content: strPtr("lo")}},
			{Done: true},
		},
	}
	var updates int
	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
		Emit: func(_ context.Context, event Event) error {
			if event.Type == EventMessageUpdate {
				updates++
			}
			return nil
		},
	}).Run(context.Background(), "stream")
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

func TestStatefulAgentTracksStreamingMessage(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		streamEvents: []provider.ChatCompletionStreamEvent{
			{Delta: provider.ChatMessageDelta{Role: "assistant"}},
			{Delta: provider.ChatMessageDelta{Content: strPtr("hel")}},
			{Delta: provider.ChatMessageDelta{Content: strPtr("lo")}},
			{Done: true},
		},
	}
	a := New(llm, tools, Config{Model: "test", Stream: true})

	sawStreaming := false
	a.Subscribe(func(_ context.Context, event Event) error {
		if event.Type == EventMessageUpdate && messageContent(event.Message) != "" {
			state := a.State()
			sawStreaming = state.StreamingMessage != nil && strings.Contains(messageContent(*state.StreamingMessage), "hel")
		}
		return nil
	})

	result, err := a.Prompt(context.Background(), "stream")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if result.Output != "hello" {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if !sawStreaming {
		t.Fatal("streaming state was not visible during message_update")
	}
	state := a.State()
	if state.StreamingMessage != nil || state.IsRunning {
		t.Fatalf("state after run = %#v, want idle without streaming message", state)
	}
}

func TestResetDoesNotAllowConcurrentPrompt(t *testing.T) {
	tools := command.NewRegistry()
	llm := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	a := New(llm, tools, Config{Model: "test"})

	done := make(chan error, 1)
	go func() {
		_, err := a.Prompt(context.Background(), "first")
		done <- err
	}()

	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}

	a.Reset()
	if _, err := a.Prompt(context.Background(), "second"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Prompt() error = %v, want already running", err)
	}

	close(llm.release)
	if err := <-done; err != nil {
		t.Fatalf("first Prompt() error = %v", err)
	}
}

func TestWaitForIdleIncludesAgentEndListeners(t *testing.T) {
	tools := command.NewRegistry()
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "done")),
		},
	}
	a := New(llm, tools, Config{Model: "test"})
	releaseListener := make(chan struct{})
	listenerStarted := make(chan struct{})
	a.Subscribe(func(_ context.Context, event Event) error {
		if event.Type == EventAgentEnd {
			close(listenerStarted)
			<-releaseListener
		}
		return nil
	})

	runDone := make(chan error, 1)
	go func() {
		_, err := a.Prompt(context.Background(), "hello")
		runDone <- err
	}()

	select {
	case <-listenerStarted:
	case <-time.After(time.Second):
		t.Fatal("agent_end listener did not start")
	}

	idleDone := make(chan struct{})
	go func() {
		a.WaitForIdle()
		close(idleDone)
	}()

	select {
	case <-idleDone:
		t.Fatal("WaitForIdle returned before agent_end listener settled")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseListener)
	select {
	case <-idleDone:
	case <-time.After(time.Second):
		t.Fatal("WaitForIdle did not return after listener settled")
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
}

func TestStreamingToolCallDeltasAreAggregated(t *testing.T) {
	tools := command.NewRegistry()
	echo := &recordingTool{name: "echo", output: "ok"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{
		streamEventBatches: [][]provider.ChatCompletionStreamEvent{
			{
				{Delta: provider.ChatMessageDelta{Role: "assistant"}},
				{Delta: provider.ChatMessageDelta{ToolCalls: []provider.ToolCallDelta{{
					Index: 0,
					ID:    "call-1",
					Type:  "function",
					Function: provider.FunctionCallDelta{
						Name:      "echo",
						Arguments: `{"value":`,
					},
				}}}},
				{Delta: provider.ChatMessageDelta{ToolCalls: []provider.ToolCallDelta{{
					Index:    0,
					Function: provider.FunctionCallDelta{Arguments: `"x"}`},
				}}}},
				{Done: true},
			},
			{
				{Delta: provider.ChatMessageDelta{Role: "assistant"}},
				{Delta: provider.ChatMessageDelta{Content: strPtr("final")}},
				{Done: true},
			},
		},
	}
	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Stream:   true,
	}).Run(context.Background(), "stream tool")
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
	tools := command.NewRegistry()
	echo := &recordingTool{name: "echo", output: "raw"}
	tools.RegisterTool(echo)
	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: provider.FunctionCall{
						Name:      "echo",
						Arguments: `{"value":"blocked"}`,
					},
				}},
			}),
		},
	}
	rewritten := "rewritten result"
	isError := false

	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		BeforeToolCall: func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error) {
			return &BeforeToolCallResult{Block: true, Reason: "blocked by test"}, nil
		},
		AfterToolCall: func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error) {
			return &AfterToolCallResult{Result: &rewritten, IsError: &isError, Flow: ToolFlowTerminate}, nil
		},
	}).Run(context.Background(), "use tool")
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

type recordingTool struct {
	name   string
	output string

	mu    sync.Mutex
	calls []string
}

func (t *recordingTool) Name() string { return t.name }

func (t *recordingTool) Description() string { return "recording tool" }

func (t *recordingTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        t.name,
			Description: t.Description(),
			Parameters:  map[string]any{"type": "object"},
		},
	}
}

func (t *recordingTool) Execute(_ context.Context, arguments string) (command.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, arguments)
	if strings.Contains(arguments, "fail") {
		return command.ToolResult{}, fmt.Errorf("failed")
	}
	return command.TextResult(t.output), nil
}

func (t *recordingTool) callsSnapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

type scriptedProvider struct {
	mu                 sync.Mutex
	responses          []*provider.ChatCompletionResponse
	err                error
	streamEvents       []provider.ChatCompletionStreamEvent
	streamEventBatches [][]provider.ChatCompletionStreamEvent
	requests           []*provider.ChatCompletionRequest
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once

	mu       sync.Mutex
	requests []*provider.ChatCompletionRequest
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return chatResponse(provider.NewTextMessage("assistant", "done")), nil
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, cloneRequest(req))
	if p.err != nil {
		return nil, p.err
	}
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *scriptedProvider) ChatCompletionStream(ctx context.Context, req *provider.ChatCompletionRequest) (<-chan provider.ChatCompletionStreamEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	events := append([]provider.ChatCompletionStreamEvent(nil), p.streamEvents...)
	if len(p.streamEventBatches) > 0 {
		events = append([]provider.ChatCompletionStreamEvent(nil), p.streamEventBatches[0]...)
		p.streamEventBatches = p.streamEventBatches[1:]
	}
	p.mu.Unlock()

	ch := make(chan provider.ChatCompletionStreamEvent)
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (p *scriptedProvider) requestsSnapshot() []*provider.ChatCompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*provider.ChatCompletionRequest, 0, len(p.requests))
	for _, req := range p.requests {
		out = append(out, cloneRequest(req))
	}
	return out
}

func chatResponse(msg provider.ChatMessage) *provider.ChatCompletionResponse {
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: msg}},
	}
}

func cloneRequest(req *provider.ChatCompletionRequest) *provider.ChatCompletionRequest {
	cloned := *req
	cloned.Messages = append([]provider.ChatMessage(nil), req.Messages...)
	cloned.Tools = append([]provider.ToolDefinition(nil), req.Tools...)
	return &cloned
}

func hasToolMessage(messages []provider.ChatMessage, toolCallID, contains string) bool {
	for _, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID != toolCallID || msg.Content == nil {
			continue
		}
		if strings.Contains(*msg.Content, contains) {
			return true
		}
	}
	return false
}

func containsEvent(events []EventType, want EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, event := range events {
		out = append(out, event.Type)
	}
	return out
}

func lastEvent(events []Event) Event {
	if len(events) == 0 {
		return Event{}
	}
	return events[len(events)-1]
}

func strPtr(s string) *string {
	return &s
}

func TestRetryOnTransientError(t *testing.T) {
	tools := command.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			callCount++
			if callCount == 1 {
				return nil, fmt.Errorf("API error (502): bad gateway")
			}
			return chatResponse(provider.NewTextMessage("assistant", "recovered")), nil
		},
	}

	result, err := (Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 2,
	}).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v, want success after retry", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("result = %q, want recovered", result.Output)
	}
	if callCount != 2 {
		t.Fatalf("call count = %d, want 2", callCount)
	}
}

func TestNoRetryOnAuthError(t *testing.T) {
	tools := command.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			callCount++
			return nil, fmt.Errorf("API error (401): invalid_api_key")
		},
	}

	_, err := (Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 3,
	}).Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("Run() error = nil, want auth error")
	}
	if callCount != 1 {
		t.Fatalf("call count = %d, want 1 (no retry for auth errors)", callCount)
	}
}

func TestRetryExhaustedReturnsLastError(t *testing.T) {
	tools := command.NewRegistry()
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			callCount++
			return nil, fmt.Errorf("API error (503): service unavailable")
		},
	}

	_, err := (Config{
		Provider:   llm,
		Tools:      tools,
		Model:      "test",
		MaxRetries: 2,
	}).Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("Run() error = nil, want error after retries exhausted")
	}
	if callCount != 3 {
		t.Fatalf("call count = %d, want 3 (1 initial + 2 retries)", callCount)
	}
}

func TestRetryableProviderTimeoutAndStallErrors(t *testing.T) {
	if !isRetryableError(fmt.Errorf("wrapped: %w", provider.ErrCallTimeout)) {
		t.Fatal("ErrCallTimeout should be retryable")
	}
	if !isRetryableError(fmt.Errorf("wrapped: %w", provider.ErrStreamStalled)) {
		t.Fatal("ErrStreamStalled should be retryable")
	}
	if !isRetryableError(retryableTimeoutError{}) {
		t.Fatal("network timeout should be retryable")
	}
	if isRetryableError(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Fatal("context.Canceled should not be retryable")
	}
	if isRetryableError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)) {
		t.Fatal("context.DeadlineExceeded should not be retryable")
	}
}

func TestStreamAssistantMessageReturnsContextErrorOnClosedCanceledStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := streamAssistantMessageWithUsage(ctx,
		&scriptedProvider{},
		&provider.ChatCompletionRequest{Model: "test"},
		nil,
		telemetry.NopLogger(),
		1,
	)
	if err != context.Canceled {
		t.Fatalf("streamAssistantMessageWithUsage() error = %v, want context.Canceled", err)
	}
}

func TestTokenBudgetWarning(t *testing.T) {
	tools := command.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			return &provider.ChatCompletionResponse{
				Choices: []provider.Choice{{Message: provider.NewTextMessage("assistant", "done")}},
				Usage:   &provider.Usage{PromptTokens: 700, CompletionTokens: 200, TotalTokens: 900},
			}, nil
		},
	}

	var sawWarning bool
	_, err := (Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
		Emit: func(_ context.Context, event Event) error {
			if event.Type == EventTokenBudgetWarning {
				sawWarning = true
			}
			return nil
		},
	}).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sawWarning {
		t.Fatal("expected token_budget_warning event at 90% usage")
	}
}

func TestTokenBudgetExceeded(t *testing.T) {
	tools := command.NewRegistry()
	turn := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			turn++
			if turn == 1 {
				return &provider.ChatCompletionResponse{
					Choices: []provider.Choice{{Message: provider.ChatMessage{
						Role: "assistant",
						ToolCalls: []provider.ToolCall{{
							ID:       "call-1",
							Type:     "function",
							Function: provider.FunctionCall{Name: "echo", Arguments: `{}`},
						}},
					}}},
					Usage: &provider.Usage{TotalTokens: 600},
				}, nil
			}
			return &provider.ChatCompletionResponse{
				Choices: []provider.Choice{{Message: provider.NewTextMessage("assistant", "done")}},
				Usage:   &provider.Usage{TotalTokens: 500},
			}, nil
		},
	}
	tools.RegisterTool(&recordingTool{name: "echo", output: "ok"})

	result, err := (Config{
		Provider:    llm,
		Tools:       tools,
		Model:       "test",
		TokenBudget: 1000,
	}).Run(context.Background(), "hello")
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
	large := strings.Repeat("x", DefaultMaxResultSize+100)
	truncated := truncateResult(large)
	if !strings.Contains(truncated, "truncated:") {
		t.Fatalf("truncated result missing size info: %s", truncated[len(truncated)-100:])
	}
	if !strings.Contains(truncated, fmt.Sprintf("%d of %d bytes", DefaultMaxResultSize, len(large))) {
		t.Fatalf("truncated result missing byte counts: %s", truncated[len(truncated)-120:])
	}
}

func TestResultIncludesTotalUsage(t *testing.T) {
	tools := command.NewRegistry()
	llm := &callbackProvider{
		fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			return &provider.ChatCompletionResponse{
				Choices: []provider.Choice{{Message: provider.NewTextMessage("assistant", "done")}},
				Usage:   &provider.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			}, nil
		},
	}

	result, err := (Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
	}).Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.TotalUsage.TotalTokens != 150 {
		t.Fatalf("TotalUsage.TotalTokens = %d, want 150", result.TotalUsage.TotalTokens)
	}
}

type callbackProvider struct {
	fn func(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error)
}

func (p *callbackProvider) Name() string { return "callback" }

func (p *callbackProvider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return p.fn(ctx, req)
}

type retryableTimeoutError struct{}

func (retryableTimeoutError) Error() string   { return "timeout awaiting response headers" }
func (retryableTimeoutError) Timeout() bool   { return true }
func (retryableTimeoutError) Temporary() bool { return true }
