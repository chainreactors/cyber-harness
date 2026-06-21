package agent

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

// TestAgentTmuxMultiRoundInteraction simulates an LLM agent using tmux
// subcommands through the bash tool to run a multi-round interactive session.
//
// Flow:
//   Turn 1: LLM calls  bash(tmux new -d -s worker "sh")
//   Turn 2: LLM calls  bash(tmux send -t worker "echo HELLO" Enter)
//   Turn 3: LLM calls  bash(tmux capture-pane -t worker --new)
//           → LLM sees HELLO in output
//   Turn 4: LLM calls  bash(tmux send -t worker "MY_VAR=42" Enter)
//   Turn 5: LLM calls  bash(tmux send -t worker "echo VAR_IS_$MY_VAR" Enter)
//   Turn 6: LLM calls  bash(tmux capture-pane -t worker --new)
//           → LLM sees VAR_IS_42 in output
//   Turn 7: LLM calls  bash(tmux send -t worker "exit" Enter)
//   Turn 8: LLM calls  bash(tmux ls)
//           → LLM sees session completed
//   Turn 9: LLM emits final text summary
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

	// Each LLM response is a single bash tool call, except the last which is text.
	// We build them in order, and verify intermediate tool results.
	var capturedRequests []*ChatCompletionRequest

	turnIndex := 0
	llm := &callbackProvider{
		fn: func(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
			capturedRequests = append(capturedRequests, cloneRequest(req))
			turnIndex++

			switch turnIndex {
			case 1:
				// Turn 1: create detached shell session
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
				// Verify turn 1 result: should contain "detached"
				assertToolResult(t, req, "call-1", "detached")

				// Wait for shell to start
				time.Sleep(500 * time.Millisecond)

				// Turn 2: send echo command
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
				// Verify turn 2 result: should contain "sent"
				assertToolResult(t, req, "call-2", "sent")

				// Wait for PTY to process the echo command
				time.Sleep(500 * time.Millisecond)

				// Turn 3: capture output
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
				// Verify turn 3 result: should contain our echo output
				assertToolResult(t, req, "call-3", "HELLO_FROM_LLM")

				// Turn 4: set a shell variable
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
				// Turn 5: echo the variable
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
				// Wait for PTY to process the echo command
				time.Sleep(500 * time.Millisecond)

				// Turn 6: capture to verify variable persisted
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
				// Verify turn 6 result: variable should have expanded
				assertToolResult(t, req, "call-6", "VAR_IS_42")

				// Turn 7: exit the shell
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
				// Turn 8: list sessions to confirm completion
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
				// Final turn: LLM produces summary
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

// TestAgentTmuxCtrlCInterrupt simulates an LLM using Ctrl-C to interrupt
// a long-running command and then continuing to interact with the session.
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
				// Start detached shell
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
				// Wait for shell to start
				time.Sleep(500 * time.Millisecond)

				// Start a long sleep
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
				// Wait for sleep to start
				time.Sleep(500 * time.Millisecond)

				// Send Ctrl-C to interrupt
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
				// Wait for Ctrl-C to take effect
				time.Sleep(500 * time.Millisecond)

				// Send a new command after interrupt
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
				// Wait for echo to produce output
				time.Sleep(500 * time.Millisecond)

				// Capture and verify recovery
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

// TestAgentTmuxInteractiveProgram simulates an LLM interacting with
// python3 REPL through tmux — multiple computation rounds then clean exit.
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
				// Start python3 REPL
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
				// Wait for python to start
				time.Sleep(800 * time.Millisecond)

				// Send first calculation
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
				// Wait for python to compute
				time.Sleep(500 * time.Millisecond)

				// Capture result
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
				// Verify 2^10 = 1024
				assertToolResult(t, req, "p3", "1024")

				// Send second calculation
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
				// Wait for python to compute
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

				// Exit python
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

func assertToolResult(t *testing.T, req *ChatCompletionRequest, toolCallID, contains string) {
	t.Helper()
	if !hasToolMessage(req.Messages, toolCallID, contains) {
		var actual string
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.ToolCallID == toolCallID && msg.Content != nil {
				actual = *msg.Content
				break
			}
		}
		t.Fatalf("tool result for %s missing %q, got: %q", toolCallID, contains, actual)
	}
}
