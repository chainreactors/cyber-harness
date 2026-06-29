package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
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
	})).Run(context.Background(), "hello")
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

	var events []EventType
	result, err := (NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus:      testBus(func(e Event) { events = append(events, e.Type) }),
	})).Run(context.Background(), "use tool")
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

func TestContinueRequiresNonAssistantLastMessage(t *testing.T) {
	tools := commands.NewRegistry()
	llm := &scriptedProvider{}
	a := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})

	if _, err := a.Continue(context.Background()); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("Continue() error = %v, want no messages", err)
	}

	a.state.Messages = []ChatMessage{NewTextMessage("assistant", "done")}
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
	if _, err := a.Run(context.Background(), "one"); err != nil {
		t.Fatalf("first prompt error = %v", err)
	}
	if _, err := a.Run(context.Background(), "two"); err != nil {
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
	tools := commands.NewRegistry()
	llm := &scriptedProvider{
		responses: []*ChatCompletionResponse{
			chatResponse(NewTextMessage("assistant", "next")),
		},
	}
	ag := NewAgent(Config{Provider: llm, Tools: tools, Model: "test"})
	ag.state.Messages = []ChatMessage{NewTextMessage("user", "base")}
	result, err := ag.Run(context.Background(), "prompt")
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
	var events []Event
	a := NewAgent(Config{
		Provider: llm,
		Tools:    tools,
		Model:    "test",
		Bus: testBus(func(event Event) {
			events = append(events, event)
		}),
	})

	result, err := a.Run(context.Background(), "hello")
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
		EventLLMRequest,
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
		_, err := a.Run(context.Background(), "first")
		done <- err
	}()

	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}

	a.Reset()
	if _, err := a.Run(context.Background(), "second"); err == nil || !strings.Contains(err.Error(), "already running") {
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

	_, err := a.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("first Run() should fail")
	}

	result, err := a.Run(context.Background(), "try again")
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
				if msg.Role == "assistant" && messageContent(msg) == "" && len(msg.ToolCalls) == 0 {
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

	a.Run(context.Background(), "hello")

	a.mu.Lock()
	for i, msg := range a.state.Messages {
		if msg.Role == "assistant" && messageContent(msg) == "" && len(msg.ToolCalls) == 0 {
			t.Errorf("state.Messages[%d] is empty assistant message", i)
		}
	}
	a.mu.Unlock()

	a.Run(context.Background(), "retry")
}

// --- Scanner integration tests ---

func TestAgentAutomaticWorkflowUsesScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	scanOutput := "[scan.summary] completed inputs 1 services 1"

	dir := t.TempDir()

	registry := commands.NewRegistry()
	registry.Register(&stubPseudoCommand{name: "scan", output: scanOutput}, "")

	bash := commands.NewBashTool(dir, 5)
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	registry.RegisterTool(bash)

	tmuxCmd := commands.NewTmuxCommand(bash.Manager())
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
	})).Run(context.Background(), "scan 127.0.0.1")
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
	task := skills.ExpandCommand("/skill:scan scan 127.0.0.1", store)

	result, err := (NewAgent(Config{
		Provider:     llm,
		Tools:        registry,
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	})).Run(context.Background(), task)
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
	if system.Role != "system" || system.Content == nil || !strings.Contains(*system.Content, "<available_skills>") {
		t.Fatalf("system prompt missing skills")
	}
	user := requests[0].Messages[1]
	if user.Role != "user" || user.Content == nil || !strings.Contains(*user.Content, `<skill name="scan"`) {
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
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash.Manager())
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
	}).Run(context.Background(), "Start an interactive shell session using tmux, test multi-round interaction")
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
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash.Manager())
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
	}).Run(context.Background(), "Test Ctrl-C interrupt in tmux session")
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
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash.Manager())
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
	}).Run(context.Background(), "Use python3 REPL via tmux to do calculations")
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
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetWorkDir(dir)
	registry.RegisterTool(bash)
	tmuxCmd := commands.NewTmuxCommand(bash.Manager())
	registry.Register(tmuxCmd, "core")
	t.Cleanup(bash.Close)

	systemPrompt := buildTmuxTestPrompt(registry)

	var events []string
	handleEvent := func(event Event) {
		switch event.Type {
		case EventToolExecutionStart:
			events = append(events, fmt.Sprintf("[TOOL] %s → %s", event.ToolName, event.Arguments))
		case EventToolExecutionEnd:
			result := event.Result
			if len(result) > 300 {
				result = result[:300] + "..."
			}
			events = append(events, fmt.Sprintf("[RESULT] %s", result))
		case EventTurnStart:
			events = append(events, fmt.Sprintf("--- Turn %d ---", event.Turn))
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
	}).Run(ctx, `Perform the following multi-round interactive test using tmux (via the bash tool).

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

Report what you observed at each step. Confirm the test passed or report failures.`)

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

	_, err := child.Run(context.Background(), "hello")
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

	var events []Event
	handler := func(e Event) {
		events = append(events, e)
	}

	tools := commands.NewRegistry()

	agentCfg := Config{
		Provider:       prov,
		Tools:          tools,
		Model:          cfg.Model,
		SystemPrompt:   systemPrompt,
		CacheRetention: CacheShort,
		Bus:            testBus(func(e Event) { handler(e) }),
		Logger:         telemetry.NopLogger(),
		MaxRetries:     1,
	}

	result1, err := NewAgent(agentCfg).Run(context.Background(), "What is 10+20? Just the number.")
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}
	t.Logf("Turn 1 output: %s", result1.Output)
	t.Logf("Turn 1 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result1.TotalUsage.PromptTokens, result1.TotalUsage.CompletionTokens,
		result1.TotalUsage.CacheReadTokens, result1.TotalUsage.CacheWriteTokens)

	if result1.Turns < 1 {
		t.Fatalf("expected at least 1 turn, got %d", result1.Turns)
	}
	if result1.TotalUsage.PromptTokens == 0 {
		t.Fatal("expected non-zero prompt tokens")
	}

	events = nil
	result2, err := NewAgent(agentCfg.WithMessages(result1.Messages)).Run(
		context.Background(),
		"What is 30+40? Just the number.",
	)
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}
	t.Logf("Turn 2 output: %s", result2.Output)
	t.Logf("Turn 2 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result2.TotalUsage.PromptTokens, result2.TotalUsage.CompletionTokens,
		result2.TotalUsage.CacheReadTokens, result2.TotalUsage.CacheWriteTokens)

	if result2.TotalUsage.PromptTokens <= result1.TotalUsage.PromptTokens {
		t.Errorf("turn 2 prompt tokens (%d) should exceed turn 1 (%d) due to accumulated context",
			result2.TotalUsage.PromptTokens, result1.TotalUsage.PromptTokens)
	}

	allMessages := append(result1.Messages, result2.NewMessages...)
	events = nil
	result3, err := NewAgent(agentCfg.WithMessages(allMessages)).Run(
		context.Background(),
		"What is the sum of all three answers you gave? Just the number.",
	)
	if err != nil {
		t.Fatalf("turn 3 failed: %v", err)
	}
	t.Logf("Turn 3 output: %s", result3.Output)
	t.Logf("Turn 3 usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result3.TotalUsage.PromptTokens, result3.TotalUsage.CompletionTokens,
		result3.TotalUsage.CacheReadTokens, result3.TotalUsage.CacheWriteTokens)

	if result3.TotalUsage.PromptTokens <= result2.TotalUsage.PromptTokens {
		t.Errorf("turn 3 prompt tokens (%d) should exceed turn 2 (%d)",
			result3.TotalUsage.PromptTokens, result2.TotalUsage.PromptTokens)
	}

	t.Logf("\n=== Multi-Turn Cache Summary ===")
	for i, r := range []*Result{result1, result2, result3} {
		ratio := 0.0
		if r.TotalUsage.PromptTokens > 0 {
			ratio = float64(r.TotalUsage.CacheReadTokens) / float64(r.TotalUsage.PromptTokens) * 100
		}
		t.Logf("Turn %d: output=%q prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			i+1, truncateOutput(r.Output, 40),
			r.TotalUsage.PromptTokens, r.TotalUsage.CacheReadTokens, r.TotalUsage.CacheWriteTokens, ratio)
	}

	totalCacheRead := result2.TotalUsage.CacheReadTokens + result3.TotalUsage.CacheReadTokens
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

	result1, err := NewAgent(agentCfg).Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("stream turn 1 failed: %v", err)
	}
	t.Logf("Stream Turn 1: output=%q prompt=%d cache_read=%d",
		truncateOutput(result1.Output, 40), result1.TotalUsage.PromptTokens, result1.TotalUsage.CacheReadTokens)

	result2, err := NewAgent(agentCfg.WithMessages(result1.Messages)).Run(context.Background(), "Goodbye")
	if err != nil {
		t.Fatalf("stream turn 2 failed: %v", err)
	}
	t.Logf("Stream Turn 2: output=%q prompt=%d cache_read=%d",
		truncateOutput(result2.Output, 40), result2.TotalUsage.PromptTokens, result2.TotalUsage.CacheReadTokens)

	allMsgs := append(result1.Messages, result2.NewMessages...)
	result3, err := NewAgent(agentCfg.WithMessages(allMsgs)).Run(context.Background(), "Thank you")
	if err != nil {
		t.Fatalf("stream turn 3 failed: %v", err)
	}
	t.Logf("Stream Turn 3: output=%q prompt=%d cache_read=%d",
		truncateOutput(result3.Output, 40), result3.TotalUsage.PromptTokens, result3.TotalUsage.CacheReadTokens)

	t.Logf("\n=== Streaming Cache Summary ===")
	for i, r := range []*Result{result1, result2, result3} {
		ratio := 0.0
		if r.TotalUsage.PromptTokens > 0 {
			ratio = float64(r.TotalUsage.CacheReadTokens) / float64(r.TotalUsage.PromptTokens) * 100
		}
		t.Logf("Turn %d: prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			i+1, r.TotalUsage.PromptTokens, r.TotalUsage.CacheReadTokens, r.TotalUsage.CacheWriteTokens, ratio)
	}
}

func TestMultiTurnWithToolCallsCache(t *testing.T) {
	cfg, prov := skipUnlessLive(t)

	systemPrompt := "You are a calculator agent. " +
		strings.Repeat("When asked to compute something, use the calculate tool. Always call the tool, never compute yourself. ", 25)

	tools := commands.NewRegistry()
	calcTool := &recordingTool{name: "calculate", output: "42"}
	tools.RegisterTool(calcTool)

	var turnEndEvents []Event
	handler := func(e Event) {
		if e.Type == EventTurnEnd {
			turnEndEvents = append(turnEndEvents, e)
		}
	}

	agentCfg := Config{
		Provider:       prov,
		Tools:          tools,
		Model:          cfg.Model,
		SystemPrompt:   systemPrompt,
		CacheRetention: CacheShort,
		Bus:            testBus(func(e Event) { handler(e) }),
		Logger:         telemetry.NopLogger(),
		MaxRetries:     1,
	}

	result, err := NewAgent(agentCfg).Run(context.Background(),
		"Use the calculate tool to compute 6*7. Then tell me the result.")
	if err != nil {
		t.Fatalf("tool call run failed: %v", err)
	}

	t.Logf("Tool-call output: %s", truncateOutput(result.Output, 80))
	t.Logf("Total turns: %d", result.Turns)
	t.Logf("Tool calls recorded: %d", len(calcTool.callsSnapshot()))

	t.Logf("\n=== Per-Turn Usage (with tool calls) ===")
	for _, tu := range result.TurnUsages {
		ratio := 0.0
		if tu.PromptTokens > 0 {
			ratio = float64(tu.CacheReadTokens) / float64(tu.PromptTokens) * 100
		}
		t.Logf("  turn %d: prompt=%d completion=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
			tu.Turn, tu.PromptTokens, tu.CompletionTokens,
			tu.CacheReadTokens, tu.CacheWriteTokens, ratio)
	}

	t.Logf("Total usage: prompt=%d completion=%d cache_read=%d cache_write=%d",
		result.TotalUsage.PromptTokens, result.TotalUsage.CompletionTokens,
		result.TotalUsage.CacheReadTokens, result.TotalUsage.CacheWriteTokens)

	if result.Turns < 2 {
		t.Logf("WARNING: expected >= 2 turns for tool call flow, got %d (model may have answered without tool)", result.Turns)
	}

	if result.Turns >= 2 && len(result.TurnUsages) >= 2 {
		laterCacheRead := result.TurnUsages[len(result.TurnUsages)-1].CacheReadTokens
		if laterCacheRead == 0 {
			t.Logf("WARNING: last turn cache_read=0 — provider may not support automatic prefix caching")
		} else {
			t.Logf("Cache working: last turn cache_read=%d", laterCacheRead)
		}
	}

	for i, e := range turnEndEvents {
		if e.Usage != nil {
			t.Logf("TurnEnd event %d: prompt=%d cache_read=%d cache_write=%d",
				i, e.Usage.PromptTokens, e.Usage.CacheReadTokens, e.Usage.CacheWriteTokens)
		}
	}
}
