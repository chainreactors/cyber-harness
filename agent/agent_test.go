package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

func TestRunWithoutToolsReturnsFinalText(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "done")),
		},
	}

	result, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
	})).Run(context.Background(), TextInput("hello"))
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
	if requests[0].Messages[0].Role != "system" || provider.MessageText(requests[0].Messages[0]) != "system" {
		t.Fatalf("system message not injected: %#v", requests[0].Messages)
	}
}

func TestRunExecutesToolLoop(t *testing.T) {
	tools := commands.NewRegistry()
	echo := &recordingTool{name: "echo", output: "tool output"}
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
		Bus:      testBus(func(e *aop.Event) { events = append(events, eventKind(e)) }),
	})).Run(context.Background(), TextInput("use tool"))
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
	if !containsEvent(events, "tool.call") || !containsEvent(events, "tool.result") {
		t.Fatalf("tool events missing: %#v", events)
	}
}

func TestContinueRequiresNonAssistantLastMessage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{}
	a := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})

	if _, err := a.Continue(context.Background()); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("Continue() error = %v, want no messages", err)
	}

	a.state.Messages = []*aop.Message{textMessage("assistant", "done")}
	if _, err := a.Continue(context.Background()); err == nil || !strings.Contains(err.Error(), "assistant") {
		t.Fatalf("Continue() error = %v, want assistant", err)
	}
}

func TestAgentReusesConversationAcrossPrompts(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "first")),
			chatResponse(NewTextMessage("assistant", "second")),
		},
	}
	a := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})
	if _, err := a.Run(context.Background(), TextInput("one")); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Run(context.Background(), TextInput("two")); err != nil {
		t.Fatalf("second prompt error = %v", err)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3: %#v", len(requests[1].Messages), requests[1].Messages)
	}
	if provider.MessageText(requests[1].Messages[0]) != "one" || provider.MessageText(requests[1].Messages[1]) != "first" || provider.MessageText(requests[1].Messages[2]) != "two" {
		t.Fatalf("unexpected reused context: %#v", requests[1].Messages)
	}
}

func TestAgentPromptReturnsRunScopedNewMessages(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "next")),
		},
	}
	ag := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})
	ag.state.Messages = []*aop.Message{textMessage("user", "base")}
	result, err := ag.Run(context.Background(), TextInput("prompt"))
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

func TestProviderErrorEmitsAgentEndAndUpdatesState(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{err: fmt.Errorf("boom")}
	var events []*aop.Event
	a := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event *aop.Event) {
			events = append(events, event)
		}),
	})

	result, err := a.Run(context.Background(), TextInput("hello"))
	if err == nil {
		t.Fatal("Prompt() error = nil, want error")
	}
	if result == nil || result.Err == nil {
		t.Fatalf("result = %#v, want result with Err", result)
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, []string{
		"message",
		"status",
		"error",
	}) {
		t.Fatalf("events = %#v", got)
	}
	if result.Turns != 1 {
		t.Fatalf("turns = %d, want 1", result.Turns)
	}
	last := lastEvent(events)
	if eventKind(last) != "error" {
		t.Fatalf("last event = %#v, want error", last)
	}
	endData := last.GetError()
	if endData == nil {
		t.Fatal("error event missing payload")
	}
	if endData.Message == "" {
		t.Fatalf("error event missing message: %+v", endData)
	}
	if a.running {
		t.Fatal("running = true, want false")
	}
	if !strings.Contains(a.state.ErrorMessage, "boom") {
		t.Fatalf("state.ErrorMessage = %q, want boom", a.state.ErrorMessage)
	}
}

func TestResetDoesNotAllowConcurrentPrompt(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	a := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})

	done := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), TextInput("first"))
		done <- err
	}()

	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}

	a.Reset()
	if _, err := a.Run(context.Background(), TextInput("second")); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Prompt() error = %v, want already running", err)
	}

	close(llm.release)
	if err := <-done; err != nil {
		t.Fatalf("first Prompt() error = %v", err)
	}
}

func TestSessionContinuesAfterLLMError(t *testing.T) {
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			if callCount == 1 {
				return nil, fmt.Errorf("API error (400): server returned bad request")
			}
			return chatResponse(NewTextMessage("assistant", "recovered")), nil
		},
	}

	a := NewAgent(Config{
		Provider:   llm,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	_, err := a.Run(context.Background(), TextInput("hello"))
	if err == nil {
		t.Fatal("first Run() should fail")
	}

	result, err := a.Run(context.Background(), TextInput("try again"))
	if err != nil {
		t.Fatalf("second Run() error = %v, want nil", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("output = %q, want 'recovered'", result.Output)
	}
}

func TestNoEmptyAssistantMessageInStateAfterError(t *testing.T) {
	callCount := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			callCount++
			if callCount == 1 {
				return nil, fmt.Errorf("boom")
			}
			for _, msg := range req.Messages {
				if msg.Role == "assistant" && messageContent(msg) == "" && len(provider.MessageToolCalls(msg)) == 0 {
					t.Errorf("found empty assistant message in request on call %d", callCount)
				}
			}
			return chatResponse(NewTextMessage("assistant", "ok")), nil
		},
	}

	a := NewAgent(Config{
		Provider:   llm,
		Model:      "test",
		MaxRetries: 0,
		Logger:     telemetry.NopLogger(),
	})

	a.Run(context.Background(), TextInput("hello"))

	a.mu.Lock()
	for i, msg := range a.state.Messages {
		if msg.Role == "assistant" && messageContent(msg) == "" && len(provider.MessageToolCalls(msg)) == 0 {
			t.Errorf("state.Messages[%d] is empty assistant message", i)
		}
	}
	a.mu.Unlock()

	a.Run(context.Background(), TextInput("retry"))
}

// --- Scanner integration tests ---

func TestAgentAutomaticWorkflowUsesScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	scanOutput := "[scan.summary] completed inputs 1 services 1"

	dir := t.TempDir()

	registry := commands.NewRegistry()
	stub := &stubPseudoCommand{name: "scan", output: scanOutput}
	registry.Register(commands.Command{Name: stub.Name(), Usage: stub.Usage(), Run: stub.Run}, "")

	bash := commands.NewBashTool(dir, 5)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)

	tmuxCmd := commands.NewTmuxCommand(bash)
	registry.Register(tmuxCmd, "core")

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: scannerBashArgs("scan -i 127.0.0.1 --mode quick"),
						},
					},
				},
			}),
			chatResponse(NewTextMessage("assistant", "final report")),
		},
	}

	systemPrompt := buildTestSystemPrompt(registry, nil)

	result, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        registry,
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	})).Run(context.Background(), TextInput("scan 127.0.0.1"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final report" {
		t.Fatalf("result = %q", result.Output)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(requests))
	}
	if !hasToolMessage(requests[1].Messages, "call-1", "[scan.summary]") {
		t.Fatalf("second request missing scan output")
	}
}

func TestAgentPromptIncludesEmbeddedSkillIndexAndExpansion(t *testing.T) {
	registry := commands.NewRegistry()
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	registry.RegisterTool(commands.NewReadTool(t.TempDir(), store))

	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "done")),
		},
	}
	systemPrompt := buildTestSystemPrompt(registry, store.Skills)
	task := skills.ExpandCommand("/skill:aiscan scan 127.0.0.1", store)

	result, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        registry,
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	})).Run(context.Background(), TextInput(task))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("result = %q", result.Output)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(requests))
	}
	system := requests[0].Messages[0]
	if system.Role != "system" || !strings.Contains(provider.MessageText(system), "<available_skills>") {
		t.Fatalf("system prompt missing skills")
	}
	user := requests[0].Messages[1]
	if user.Role != "user" || !strings.Contains(provider.MessageText(user), `<skill name="aiscan"`) {
		t.Fatalf("user prompt missing expanded skill")
	}
}

// --- Tmux integration tests ---

func TestAgentTmuxMultiRoundInteraction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	dir := t.TempDir()
	registry := commands.NewRegistry()
	bash := commands.NewBashTool(dir, 30)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash)
	registry.Register(tmuxCmd, "core")
	t.Cleanup(bash.Close)

	var capturedRequests []*ChatCompletionRequest

	turnIndex := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			capturedRequests = append(capturedRequests, cloneRequest(req))
			turnIndex++

			switch turnIndex {
			case 1:
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-1", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux new -d -s worker "sh"`),
						},
					}},
				}), nil

			case 2:
				assertToolResult(t, req, "call-1", "detached")
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-2", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t worker "echo HELLO_FROM_LLM" Enter`),
						},
					}},
				}), nil

			case 3:
				assertToolResult(t, req, "call-2", "sent")
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-3", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux capture-pane -t worker --new`),
						},
					}},
				}), nil

			case 4:
				assertToolResult(t, req, "call-3", "HELLO_FROM_LLM")
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-4", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t worker "MY_VAR=42" Enter`),
						},
					}},
				}), nil

			case 5:
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-5", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t worker "echo VAR_IS_$MY_VAR" Enter`),
						},
					}},
				}), nil

			case 6:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-6", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux capture-pane -t worker --new`),
						},
					}},
				}), nil

			case 7:
				assertToolResult(t, req, "call-6", "VAR_IS_42")
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-7", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t worker "exit" Enter`),
						},
					}},
				}), nil

			case 8:
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "call-8", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux ls`),
						},
					}},
				}), nil

			case 9:
				return chatResponse(NewTextMessage("assistant",
					"Interactive session completed. Verified: echo output, shell variable persistence, and clean exit.")), nil

			default:
				t.Fatalf("unexpected turn %d", turnIndex)
				return nil, nil
			}
		},
	}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    registry,
		Model:    "test",
	}).Run(context.Background(), TextInput("Start an interactive shell session using tmux, test multi-round interaction"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !strings.Contains(result.Output, "Interactive session completed") {
		t.Fatalf("unexpected final output: %q", result.Output)
	}
	if turnIndex != 9 {
		t.Fatalf("expected 9 turns, got %d", turnIndex)
	}
	t.Logf("Agent completed %d turns of tmux interaction successfully", turnIndex)
}

func TestAgentTmuxCtrlCInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	dir := t.TempDir()
	registry := commands.NewRegistry()
	bash := commands.NewBashTool(dir, 30)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash)
	registry.Register(tmuxCmd, "core")
	t.Cleanup(bash.Close)

	turnIndex := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			turnIndex++
			switch turnIndex {
			case 1:
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "c1", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux new -d -s runner "sh"`),
						},
					}},
				}), nil
			case 2:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "c2", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t runner "sleep 999" Enter`),
						},
					}},
				}), nil
			case 3:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "c3", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t runner C-c`),
						},
					}},
				}), nil
			case 4:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "c4", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t runner "echo RECOVERED" Enter`),
						},
					}},
				}), nil
			case 5:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "c5", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux capture-pane -t runner --new`),
						},
					}},
				}), nil
			case 6:
				assertToolResult(t, req, "c5", "RECOVERED")
				return chatResponse(NewTextMessage("assistant", "Ctrl-C interrupt and recovery verified.")), nil
			default:
				t.Fatalf("unexpected turn %d", turnIndex)
				return nil, nil
			}
		},
	}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    registry,
		Model:    "test",
	}).Run(context.Background(), TextInput("Test Ctrl-C interrupt in tmux session"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Output, "Ctrl-C interrupt") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	t.Logf("Ctrl-C interrupt test passed in %d turns", turnIndex)
}

func TestAgentTmuxInteractiveProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}

	dir := t.TempDir()
	registry := commands.NewRegistry()
	bash := commands.NewBashTool(dir, 30)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash)
	registry.Register(tmuxCmd, "core")
	t.Cleanup(bash.Close)

	turnIndex := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			turnIndex++
			switch turnIndex {
			case 1:
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p1", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux new -d -s pyrepl "python3 -u -i"`),
						},
					}},
				}), nil
			case 2:
				time.Sleep(800 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p2", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t pyrepl "print(2**10)" Enter`),
						},
					}},
				}), nil
			case 3:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p3", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux capture-pane -t pyrepl --new`),
						},
					}},
				}), nil
			case 4:
				assertToolResult(t, req, "p3", "1024")
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p4", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t pyrepl "print('hello' + ' ' + 'world')" Enter`),
						},
					}},
				}), nil
			case 5:
				time.Sleep(500 * time.Millisecond)
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p5", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux capture-pane -t pyrepl --new`),
						},
					}},
				}), nil
			case 6:
				assertToolResult(t, req, "p5", "hello world")
				return chatResponse(ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID: "p6", Type: "function",
						Function: FunctionCall{
							Name:      "bash",
							Arguments: bashArgs(`tmux send -t pyrepl "exit()" Enter`),
						},
					}},
				}), nil
			case 7:
				return chatResponse(NewTextMessage("assistant",
					"Python REPL interaction verified: 2^10=1024, string concat, clean exit.")), nil
			default:
				t.Fatalf("unexpected turn %d", turnIndex)
				return nil, nil
			}
		},
	}

	result, err := NewAgent(Config{
		Provider: llm,
		Tools:    registry,
		Model:    "test",
	}).Run(context.Background(), TextInput("Use python3 REPL via tmux to do calculations"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Output, "Python REPL") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	t.Logf("Python REPL interaction test passed in %d turns", turnIndex)
}

func TestLiveLLMTmuxInteraction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	baseURL := envOr("LIVE_TEST_BASE_URL", "https://api.deepseek.com")
	apiKey := os.Getenv("LIVE_TEST_API_KEY")
	model := envOr("LIVE_TEST_MODEL", "deepseek-chat")

	if apiKey == "" {
		t.Skip("no LIVE_TEST_API_KEY set; skipping live LLM test")
	}

	llm, err := NewProvider(&ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 120,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	dir := t.TempDir()
	registry := commands.NewRegistry()
	bash := commands.NewBashTool(dir, 60)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash)
	registry.Register(tmuxCmd, "core")
	t.Cleanup(bash.Close)

	systemPrompt := buildTmuxTestPrompt(registry)

	var events []string
	handleEvent := func(event *aop.Event) {
		switch eventKind(event) {
		case "tool.call":
			if data := event.GetToolCall(); data != nil {
				events = append(events, fmt.Sprintf("[TOOL] %s → %s", data.Name, data.GetArguments().GetData()))
			}
		case "tool.result":
			if data := event.GetToolResult(); data != nil {
				result := fmt.Sprintf("%v", data.Output)
				if len(result) > 300 {
					result = result[:300] + "..."
				}
				events = append(events, fmt.Sprintf("[RESULT] %s", result))
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := NewAgent(Config{
		Provider:     llm,
		Tools:        registry,
		Model:        model,
		SystemPrompt: systemPrompt,
		Bus:          testBus(handleEvent),
		MaxRetries:   2,
	}).Run(ctx, TextInput(`Perform the following multi-round interactive test using tmux (via the bash tool).

Execute these steps IN ORDER, one bash tool call per step:

Step 1: tmux new -d -s test_sess "sh"
Step 2: sleep 0.3
Step 3: tmux send -t test_sess "echo HELLO_WORLD" Enter
Step 4: sleep 0.3
Step 5: tmux capture-pane -t test_sess --new
        → You should see HELLO_WORLD in the output
Step 6: tmux send -t test_sess "MY_VAR=MAGIC_42" Enter
Step 7: sleep 0.2
Step 8: tmux send -t test_sess "echo RESULT_IS_$MY_VAR" Enter
Step 9: sleep 0.3
Step 10: tmux capture-pane -t test_sess --new
         → You should see RESULT_IS_MAGIC_42 in the output
Step 11: tmux send -t test_sess "exit" Enter
Step 12: sleep 0.3
Step 13: tmux ls
         → Session should show as completed

Report what you observed at each step. Confirm the test passed or report failures.`))

	t.Log("\n=== Event Log ===")
	for _, e := range events {
		t.Log(e)
	}
	if result != nil {
		t.Log("\n=== LLM Final Output ===")
		t.Log(result.Output)
		t.Logf("Turns: %d, Total tokens: %d", result.Turns, result.TotalUsage.TotalTokens)
	}

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	combinedLog := strings.Join(events, "\n")
	if result != nil {
		combinedLog += "\n" + result.Output
	}

	for _, want := range []string{"HELLO_WORLD", "MAGIC_42"} {
		if !strings.Contains(combinedLog, want) {
			t.Errorf("expected %q in output/events but not found", want)
		}
	}
}

// --- Cache/Config tests ---

func TestCacheConfigInheritance(t *testing.T) {
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "done")),
		},
	}

	parentCfg := Config{
		Provider:       llm,
		Tools:          commands.NewRegistry(),
		Model:          "test",
		SystemPrompt:   "sys",
		CacheRetention: CacheShort,
		SessionID:      "parent-session-123",
	}

	child := NewAgent(parentCfg).Derive()

	if child.Cfg.CacheRetention != CacheShort {
		t.Errorf("child CacheRetention = %q, want %q", child.Cfg.CacheRetention, CacheShort)
	}
	if child.Cfg.SessionID == "" {
		t.Error("child SessionID should be auto-generated, got empty")
	}
	if child.Cfg.SessionID == "parent-session-123" {
		t.Error("child SessionID should differ from parent")
	}

	_, err := child.Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatal(err)
	}

	reqs := llm.requestsSnapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].CacheRetention != CacheShort {
		t.Errorf("request CacheRetention = %q, want %q", reqs[0].CacheRetention, CacheShort)
	}
	if reqs[0].SessionID != child.Cfg.SessionID {
		t.Errorf("request SessionID = %q, want child SessionID %q", reqs[0].SessionID, child.Cfg.SessionID)
	}
}

func TestSessionIDAutoGeneration(t *testing.T) {
	cfg := Config{
		CacheRetention: CacheShort,
	}
	initialized := cfg.init()

	if initialized.SessionID == "" {
		t.Error("expected auto-generated SessionID, got empty")
	}
	if len(initialized.SessionID) != 16 {
		t.Errorf("SessionID length = %d, want 16 hex chars, got %q", len(initialized.SessionID), initialized.SessionID)
	}

	cfg2 := Config{CacheRetention: CacheNone}
	initialized2 := cfg2.init()
	if initialized2.SessionID == "" {
		t.Error("CacheNone should still generate SessionID for event tracking")
	}
}

// --- Live cache integration tests ---

func TestMultiTurnContextInheritanceAndCache(t *testing.T) {
	cfg, prov := skipUnlessLive(t)

	systemPrompt := "You are a math tutor. " +
		strings.Repeat("You always answer arithmetic questions with just the numeric result. ", 30)

	var events []*aop.Event
	handler := func(e *aop.Event) {
		events = append(events, e)
	}

	tools := commands.NewRegistry()

	agentCfg := Config{
		Provider:       prov,
		Tools:          tools,
		Model:          cfg.Model,
		SystemPrompt:   systemPrompt,
		CacheRetention: CacheShort,
		Bus:            testBus(func(e *aop.Event) { handler(e) }),
		Logger:         telemetry.NopLogger(),
		MaxRetries:     1,
	}

	result1, err := NewAgent(agentCfg).Run(context.Background(), TextInput("What is 10+20? Just the number."))
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}
	t.Logf("Turn 1 output: %s", result1.Output)
	t.Logf("Turn 1 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result1.TotalUsage.InputTokens, result1.TotalUsage.OutputTokens,
		result1.TotalUsage.Detail["cache_read"], result1.TotalUsage.Detail["cache_write"])

	if result1.Turns < 1 {
		t.Fatalf("expected at least 1 turn, got %d", result1.Turns)
	}
	if result1.TotalUsage.InputTokens == 0 {
		t.Fatal("expected non-zero prompt tokens")
	}

	events = nil
	result2, err := NewAgent(agentCfg.WithMessages(result1.Messages)).Run(
		context.Background(),
		TextInput("What is 30+40? Just the number."),
	)
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}
	t.Logf("Turn 2 output: %s", result2.Output)
	t.Logf("Turn 2 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result2.TotalUsage.InputTokens, result2.TotalUsage.OutputTokens,
		result2.TotalUsage.Detail["cache_read"], result2.TotalUsage.Detail["cache_write"])

	if result2.TotalUsage.InputTokens <= result1.TotalUsage.InputTokens {
		t.Errorf("turn 2 prompt tokens (%d) should exceed turn 1 (%d) due to accumulated context",
			result2.TotalUsage.InputTokens, result1.TotalUsage.InputTokens)
	}

	allMessages := append(result1.Messages, result2.NewMessages...)
	events = nil
	result3, err := NewAgent(agentCfg.WithMessages(allMessages)).Run(
		context.Background(),
		TextInput("What is the sum of all three answers you gave? Just the number."),
	)
	if err != nil {
		t.Fatalf("turn 3 failed: %v", err)
	}
	t.Logf("Turn 3 output: %s", result3.Output)
	t.Logf("Turn 3 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result3.TotalUsage.InputTokens, result3.TotalUsage.OutputTokens,
		result3.TotalUsage.Detail["cache_read"], result3.TotalUsage.Detail["cache_write"])

	if result3.TotalUsage.InputTokens <= result2.TotalUsage.InputTokens {
		t.Errorf("turn 3 prompt tokens (%d) should exceed turn 2 (%d)",
			result3.TotalUsage.InputTokens, result2.TotalUsage.InputTokens)
	}

	t.Logf("\n=== Multi-Turn Cache Summary ===")
	for i, r := range []*Result{result1, result2, result3} {
		ratio := 0.0
		if r.TotalUsage.InputTokens > 0 {
			ratio = float64(r.TotalUsage.Detail["cache_read"]) / float64(r.TotalUsage.InputTokens) * 100
		}
		t.Logf("Turn %d: output=%q prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			i+1, truncateOutput(r.Output, 40),
			r.TotalUsage.InputTokens, r.TotalUsage.Detail["cache_read"], r.TotalUsage.Detail["cache_write"], ratio)
	}

	totalCacheRead := result2.TotalUsage.Detail["cache_read"] + result3.TotalUsage.Detail["cache_read"]
	if totalCacheRead == 0 {
		t.Error("expected cache_read > 0 in turn 2 or 3, got 0 for both — caching may not be working")
	}
}

func TestMultiTurnStreamingCache(t *testing.T) {
	cfg, prov := skipUnlessLive(t)

	systemPrompt := "You are a translator. " +
		strings.Repeat("You translate English to French. Always respond with just the translation, nothing else. ", 30)

	tools := commands.NewRegistry()

	agentCfg := Config{
		Provider:       prov,
		Tools:          tools,
		Model:          cfg.Model,
		SystemPrompt:   systemPrompt,
		Stream:         true,
		CacheRetention: CacheShort,
		Logger:         telemetry.NopLogger(),
		MaxRetries:     1,
	}

	result1, err := NewAgent(agentCfg).Run(context.Background(), TextInput("Hello"))
	if err != nil {
		t.Fatalf("stream turn 1 failed: %v", err)
	}
	t.Logf("Stream Turn 1: output=%q prompt=%d cache_read=%d",
		truncateOutput(result1.Output, 40), result1.TotalUsage.InputTokens, result1.TotalUsage.Detail["cache_read"])

	result2, err := NewAgent(agentCfg.WithMessages(result1.Messages)).Run(context.Background(), TextInput("Goodbye"))
	if err != nil {
		t.Fatalf("stream turn 2 failed: %v", err)
	}
	t.Logf("Stream Turn 2: output=%q prompt=%d cache_read=%d",
		truncateOutput(result2.Output, 40), result2.TotalUsage.InputTokens, result2.TotalUsage.Detail["cache_read"])

	allMsgs := append(result1.Messages, result2.NewMessages...)
	result3, err := NewAgent(agentCfg.WithMessages(allMsgs)).Run(context.Background(), TextInput("Thank you"))
	if err != nil {
		t.Fatalf("stream turn 3 failed: %v", err)
	}
	t.Logf("Stream Turn 3: output=%q prompt=%d cache_read=%d",
		truncateOutput(result3.Output, 40), result3.TotalUsage.InputTokens, result3.TotalUsage.Detail["cache_read"])

	t.Logf("\n=== Streaming Cache Summary ===")
	for i, r := range []*Result{result1, result2, result3} {
		ratio := 0.0
		if r.TotalUsage.InputTokens > 0 {
			ratio = float64(r.TotalUsage.Detail["cache_read"]) / float64(r.TotalUsage.InputTokens) * 100
		}
		t.Logf("Turn %d: prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			i+1, r.TotalUsage.InputTokens, r.TotalUsage.Detail["cache_read"], r.TotalUsage.Detail["cache_write"], ratio)
	}
}

func TestMultiTurnWithToolCallsCache(t *testing.T) {
	cfg, prov := skipUnlessLive(t)

	systemPrompt := "You are a calculator agent. " +
		strings.Repeat("When asked to compute something, use the calculate tool. Always call the tool, never compute yourself. ", 25)

	tools := commands.NewRegistry()
	calcTool := &recordingTool{name: "calculate", output: "42"}
	tools.RegisterTool(calcTool)

	var usageEvents []*aop.Event
	handler := func(e *aop.Event) {
		if eventKind(e) == "usage" {
			usageEvents = append(usageEvents, e)
		}
	}

	agentCfg := Config{
		Provider:       prov,
		Tools:          tools,
		Model:          cfg.Model,
		SystemPrompt:   systemPrompt,
		CacheRetention: CacheShort,
		Bus:            testBus(func(e *aop.Event) { handler(e) }),
		Logger:         telemetry.NopLogger(),
		MaxRetries:     1,
	}

	result, err := NewAgent(agentCfg).Run(context.Background(),
		TextInput("Use the calculate tool to compute 6*7. Then tell me the result."))
	if err != nil {
		t.Fatalf("tool call run failed: %v", err)
	}

	t.Logf("Tool-call output: %s", truncateOutput(result.Output, 80))
	t.Logf("Total turns: %d", result.Turns)
	t.Logf("Tool calls recorded: %d", len(calcTool.callsSnapshot()))

	t.Logf("\n=== Per-Turn Usage (with tool calls) ===")
	for i, tu := range result.TurnUsages {
		ratio := 0.0
		if tu.InputTokens > 0 {
			ratio = float64(tu.Detail["cache_read"]) / float64(tu.InputTokens) * 100
		}
		t.Logf("  turn %d: prompt=%d completion=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			i+1, tu.InputTokens, tu.OutputTokens,
			tu.Detail["cache_read"], tu.Detail["cache_write"], ratio)
	}

	t.Logf("Total usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result.TotalUsage.InputTokens, result.TotalUsage.OutputTokens,
		result.TotalUsage.Detail["cache_read"], result.TotalUsage.Detail["cache_write"])

	if result.Turns < 2 {
		t.Logf("WARNING: expected >= 2 turns for tool call flow, got %d (model may have answered without tool)", result.Turns)
	}

	if result.Turns >= 2 && len(result.TurnUsages) >= 2 {
		laterCacheRead := result.TurnUsages[len(result.TurnUsages)-1].Detail["cache_read"]
		if laterCacheRead == 0 {
			t.Logf("WARNING: last turn cache_read=0 — provider may not support automatic prefix caching")
		} else {
			t.Logf("Cache working: last turn cache_read=%d", laterCacheRead)
		}
	}

	for i, e := range usageEvents {
		if data := e.GetUsage(); data != nil {
			t.Logf("Usage event %d: prompt=%d cache_read=%d cache_write=%d",
				i, data.InputTokens, data.Detail["cache_read"], data.Detail["cache_write"])
		}
	}
}

// TestSetProviderHotSwapsNextRun verifies a mid-conversation provider swap takes
// effect on the next run (an in-flight run keeps its snapshotted provider).
func TestSetProviderHotSwapsNextRun(t *testing.T) {
	provA := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "from-A")), nil
	}}
	provB := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "from-B")), nil
	}}

	ag := NewAgent(Config{Provider: provA, Model: "model-a"})

	res, err := ag.Run(context.Background(), TextInput("hi"))
	if err != nil {
		t.Fatalf("run A: %v", err)
	}
	if res.Output != "from-A" {
		t.Fatalf("run A output = %q, want from-A", res.Output)
	}

	ag.SetProvider(provB, "model-b")

	res, err = ag.Run(context.Background(), TextInput("hi again"))
	if err != nil {
		t.Fatalf("run B: %v", err)
	}
	if res.Output != "from-B" {
		t.Fatalf("run B output = %q, want from-B", res.Output)
	}
	if ag.Cfg.Model != "model-b" {
		t.Fatalf("model = %q, want model-b", ag.Cfg.Model)
	}

	// Empty model must not blank the current one (provider-only swap).
	ag.SetProvider(provA, "")
	if ag.Cfg.Model != "model-b" {
		t.Fatalf("empty-model swap changed model to %q, want model-b", ag.Cfg.Model)
	}
}

func TestSetProviderConfigHotSwapsModelLimits(t *testing.T) {
	provider := &scriptedProvider{}
	ag := NewAgent(Config{MaxTokens: 1024, ContextWindow: 8192})
	ag.SetProviderConfig(provider, ProviderConfig{
		Model: "glm-5.2[1m]", MaxTokens: 32768, ContextWindow: 1000000,
	})
	cfg := ag.configSnapshot()
	if cfg.Provider != provider || cfg.Model != "glm-5.2[1m]" || cfg.MaxTokens != 32768 || cfg.ContextWindow != 1000000 {
		t.Fatalf("hot-swapped config = %+v", cfg)
	}
}

// TestSetProviderRaceWithRun exercises a config push swapping the provider while
// runs execute; run under -race it proves the Cfg read/write are serialized.
func TestSetProviderRaceWithRun(t *testing.T) {
	prov := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "ok")), nil
	}}
	ag := NewAgent(Config{Provider: prov, Model: "m"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			ag.SetProvider(prov, fmt.Sprintf("m-%d", i))
		}
	}()
	for i := 0; i < 50; i++ {
		if _, err := ag.Run(context.Background(), TextInput("hi")); err != nil {
			t.Errorf("run %d: %v", i, err)
		}
	}
	<-done
}

// --- Legacy message construction shims -------------------------------------
// These mirror the pre-proto ChatMessage shape so the many construction sites
// in the tests stay readable; chatResponse converts them to *aop.Message.

type FunctionCall struct {
	Name      string
	Arguments string
}

type ToolCall struct {
	ID       string
	Type     string
	Function FunctionCall
}

type ChatMessage struct {
	Role      string
	Content   *string
	ToolCalls []ToolCall
}

func (m ChatMessage) toAOP() *aop.Message {
	msg := &aop.Message{Role: m.Role}
	if m.Content != nil {
		msg.Content = append(msg.Content, aop.Text(*m.Content))
	}
	for _, c := range m.ToolCalls {
		msg.Content = append(msg.Content, toolCallContent(c.ID, c.Function.Name, c.Function.Arguments))
	}
	return msg
}

func toolCallContent(id, name, args string) *aop.Content {
	return &aop.Content{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
		Id:   id,
		Name: name,
		Kind: "function",
		Arguments: &aop.EncodedValue{
			Data:      []byte(args),
			MediaType: aop.JSONMediaType,
		},
	}}}
}

func NewTextMessage(role, text string) ChatMessage {
	return ChatMessage{Role: role, Content: &text}
}

func textMessage(role, text string) *aop.Message {
	return provider.TextMessage(role, text)
}

func toolResultMessage(callID, output string) *aop.Message {
	return provider.ToolResultMessage(callID, tool.TextResult(output))
}

func imageMessage(role string, parts ...*aop.Content) *aop.Message {
	return &aop.Message{Role: role, Content: parts}
}

// --- Streaming event shims --------------------------------------------------

func roleDelta(role string) ChatCompletionStreamEvent {
	return ChatCompletionStreamEvent{Role: role}
}

func textDelta(s string) ChatCompletionStreamEvent {
	return ChatCompletionStreamEvent{MessageDelta: &aop.MessageDelta{
		Value: &aop.MessageDelta_Text{Text: s},
	}}
}

func reasoningDelta(s string) ChatCompletionStreamEvent {
	return ChatCompletionStreamEvent{MessageDelta: &aop.MessageDelta{
		Value: &aop.MessageDelta_Reasoning{Reasoning: s},
	}}
}

func toolCallDelta(index uint32, id, name, args string) ChatCompletionStreamEvent {
	return ChatCompletionStreamEvent{ToolDeltas: []*aop.ToolCallDelta{{
		Index:     index,
		CallId:    id,
		Name:      name,
		Arguments: []byte(args),
	}}}
}

func testBus(handler func(*aop.Event)) *eventbus.Bus[*aop.Event] {
	b := eventbus.New[*aop.Event]()
	if handler != nil {
		b.Subscribe(handler)
	}
	return b
}

type recordingTool struct {
	name   string
	output string

	mu    sync.Mutex
	calls []string
}

func (t *recordingTool) Name() string { return t.name }

func (t *recordingTool) Description() string { return "recording tool" }

func (t *recordingTool) Definition() *aop.ToolDefinition {
	return tool.Def(t.name, t.Description(), struct{}{})
}

func (t *recordingTool) Execute(_ context.Context, arguments string) (*tool.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, arguments)
	if strings.Contains(arguments, "fail") {
		return nil, fmt.Errorf("failed")
	}
	return tool.TextResult(t.output), nil
}

func (t *recordingTool) callsSnapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

type scriptedProvider struct {
	mu                 sync.Mutex
	responses          []*ChatCompletionResponse
	err                error
	streamEvents       []ChatCompletionStreamEvent
	streamEventBatches [][]ChatCompletionStreamEvent
	requests           []*ChatCompletionRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
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

func (p *scriptedProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	events := append([]ChatCompletionStreamEvent(nil), p.streamEvents...)
	if len(p.streamEventBatches) > 0 {
		events = append([]ChatCompletionStreamEvent(nil), p.streamEventBatches[0]...)
		p.streamEventBatches = p.streamEventBatches[1:]
	}
	p.mu.Unlock()

	ch := make(chan ChatCompletionStreamEvent)
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

func (p *scriptedProvider) requestsSnapshot() []*ChatCompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*ChatCompletionRequest, 0, len(p.requests))
	for _, req := range p.requests {
		out = append(out, cloneRequest(req))
	}
	return out
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once

	mu       sync.Mutex
	requests []*ChatCompletionRequest
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return chatResponse(NewTextMessage("assistant", "done")), nil
}

type callbackProvider struct {
	fn func(context.Context, *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

func (p *callbackProvider) Name() string { return "callback" }

func (p *callbackProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return p.fn(ctx, req)
}

type retryableTimeoutError struct{}

func (retryableTimeoutError) Error() string   { return "timeout awaiting response headers" }
func (retryableTimeoutError) Timeout() bool   { return true }
func (retryableTimeoutError) Temporary() bool { return true }

type imageErrorProvider struct {
	imagesDisabled atomic.Bool
	callCount      atomic.Int32
}

func (p *imageErrorProvider) Name() string { return "image-error" }

func (p *imageErrorProvider) DisableImages() {
	p.imagesDisabled.Store(true)
}

func (p *imageErrorProvider) ChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.callCount.Add(1)
	if p.imagesDisabled.Load() || !messagesContainImages(req.Messages) {
		return chatResponse(NewTextMessage("assistant", "success without images")), nil
	}
	return nil, &APIError{StatusCode: 400, Message: "Invalid parameter: messages[5].content[1].type is not supported, unknown type: image_url"}
}

func messagesContainImages(msgs []*aop.Message) bool {
	for _, m := range msgs {
		for _, p := range m.Content {
			if p.GetMedia() != nil {
				return true
			}
			if r := p.GetToolResult(); r != nil {
				for _, block := range r.Output {
					if block.GetMedia() != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

type pushingProvider struct {
	inner  Provider
	inbox  *inbox.Buffered
	pushed bool
	push   inbox.Message
}

func (p *pushingProvider) Name() string { return "pushing" }

func (p *pushingProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if !p.pushed {
		p.pushed = true
		p.inbox.Push(p.push)
	}
	return p.inner.ChatCompletion(ctx, req)
}

type stubPseudoCommand struct {
	name   string
	output string
}

func (c *stubPseudoCommand) Name() string  { return c.name }
func (c *stubPseudoCommand) Usage() string { return c.name }
func (c *stubPseudoCommand) Run(_ context.Context, execution *commands.Execution) (any, error) {
	fmt.Fprint(execution.Stdout, c.output)
	return nil, nil
}

func chatResponse(msg ChatMessage) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		Choices: []Choice{{Message: msg.toAOP()}},
	}
}

func cloneRequest(req *ChatCompletionRequest) *ChatCompletionRequest {
	cloned := *req
	cloned.Messages = append([]*aop.Message(nil), req.Messages...)
	cloned.Tools = append([]*aop.ToolDefinition(nil), req.Tools...)
	return &cloned
}

func hasToolMessage(messages []*aop.Message, toolCallID, contains string) bool {
	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		r := provider.MessageToolResult(msg)
		if r == nil || r.CallId != toolCallID {
			continue
		}
		if strings.Contains(tool.ResultText(r), contains) {
			return true
		}
	}
	return false
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []*aop.Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, aop.Kind(event))
	}
	return out
}

func eventKind(event *aop.Event) string { return aop.Kind(event) }

func lastEvent(events []*aop.Event) *aop.Event {
	if len(events) == 0 {
		return nil
	}
	return events[len(events)-1]
}

func messageContent(m *aop.Message) string {
	return provider.MessageText(m)
}

func contentOf(m *aop.Message) string {
	return provider.MessageText(m)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func scannerBashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func assertToolResult(t *testing.T, req *ChatCompletionRequest, toolCallID, contains string) {
	t.Helper()
	if !hasToolMessage(req.Messages, toolCallID, contains) {
		var actual string
		for _, msg := range req.Messages {
			if msg.Role != "tool" {
				continue
			}
			if r := provider.MessageToolResult(msg); r != nil && r.CallId == toolCallID {
				actual = tool.ResultText(r)
				break
			}
		}
		t.Fatalf("tool result for %s missing %q, got: %q", toolCallID, contains, actual)
	}
}

func buildTestSystemPrompt(tools *commands.CommandRegistry, ss []skills.Skill) string {
	var sb strings.Builder
	sb.WriteString("You are a test agent.\n\n## Available Tools\n\n")
	if tools != nil {
		for _, t := range tools.Tools() {
			sb.WriteString("### " + t.Name() + "\n" + t.Description() + "\n\n")
		}
		if docs := tools.UsageDocs(); docs != "" {
			sb.WriteString("## Pseudo-Commands\n\n" + docs + "\n\n")
		}
	}
	if skillPrompt := skills.FormatForPrompt(ss); skillPrompt != "" {
		sb.WriteString(skillPrompt)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func buildTmuxTestPrompt(registry *commands.CommandRegistry) string {
	var sb strings.Builder
	sb.WriteString("You are a test agent. You have one tool: bash.\n\n## Tool: bash\n")
	for _, tool := range registry.Tools() {
		sb.WriteString(tool.Description())
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Pseudo-Commands (use via bash tool)\n\ntmux is a pseudo-command built into the bash tool. Call it like:\n  bash tool call with {\"command\": \"tmux new -d -s myname \\\"sh\\\"\"}\n  bash tool call with {\"command\": \"tmux send -t myname \\\"echo hi\\\" Enter\"}\n  bash tool call with {\"command\": \"tmux capture-pane -t myname --new\"}\n  bash tool call with {\"command\": \"tmux ls\"}\n  bash tool call with {\"command\": \"tmux kill -t myname\"}\n\ntmux usage:\n")
	sb.WriteString(registry.UsageDocs())

	sb.WriteString("\n## Rules\n\n1. Execute ONE bash call per step. Do not combine multiple steps.\n2. After send-keys, always sleep briefly (sleep 0.3) before capture-pane.\n3. Use capture-pane with --new for incremental output.\n4. Report observations at the end.\n")
	return sb.String()
}

func skipUnlessLive(t *testing.T) (*ProviderConfig, Provider) {
	t.Helper()
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL to run live tests")
	}
	cfg := &ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 60,
	}
	cfg, err := ResolveProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := NewProviderFromResolved(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, prov
}

func truncateOutput(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestRootAgentPublicImport(t *testing.T) {
	config := Config{}.
		WithModel("example-model").
		WithMaxTokens(256).
		WithContextWindow(4096)
	if config.Model != "example-model" || config.MaxTokens != 256 || config.ContextWindow != 4096 {
		t.Fatalf("root agent config aliases/builders are not externally usable: %#v", config)
	}
	_ = ProviderConfig{Model: "example-model"}
}
