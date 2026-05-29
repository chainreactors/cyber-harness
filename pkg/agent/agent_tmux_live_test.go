package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/command"
)

// TestLiveLLMTmuxInteraction uses a real LLM to drive tmux multi-round
// interactive sessions through the bash tool, exactly as the agent would
// in production.
//
// Set environment variables to run:
//
//	LIVE_TEST_BASE_URL=https://api.deepseek.com \
//	LIVE_TEST_API_KEY=sk-xxx \
//	LIVE_TEST_MODEL=deepseek-chat \
//	go test -v -run TestLiveLLMTmuxInteraction ./pkg/agent/ -timeout 300s
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

	llm, err := provider.NewProvider(&provider.ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 120,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	dir := t.TempDir()
	registry := command.NewRegistry()
	bash := command.NewBashTool(dir, 60, registry)
	registry.RegisterTool(bash)
	tmux := command.NewTmuxCommand(bash.Manager(), registry)
	registry.Register(tmux, "core")
	t.Cleanup(bash.Close)

	systemPrompt := buildTmuxTestPrompt(registry)

	var events []string
	emit := func(_ context.Context, event Event) error {
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
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := (Config{
		Provider:     llm,
		Tools:        registry,
		Model:        model,
		SystemPrompt: systemPrompt,
		Emit:         emit,
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

func buildTmuxTestPrompt(registry *command.CommandRegistry) string {
	var sb strings.Builder
	sb.WriteString(`You are a test agent. You have one tool: bash.

## Tool: bash
`)
	for _, tool := range registry.Tools() {
		sb.WriteString(tool.Description())
		sb.WriteString("\n\n")
	}

	sb.WriteString(`## Pseudo-Commands (use via bash tool)

tmux is a pseudo-command built into the bash tool. Call it like:
  bash tool call with {"command": "tmux new -d -s myname \"sh\""}
  bash tool call with {"command": "tmux send -t myname \"echo hi\" Enter"}
  bash tool call with {"command": "tmux capture-pane -t myname --new"}
  bash tool call with {"command": "tmux ls"}
  bash tool call with {"command": "tmux kill -t myname"}

tmux usage:
`)
	sb.WriteString(registry.UsageDocs())

	sb.WriteString(`
## Rules

1. Execute ONE bash call per step. Do not combine multiple steps.
2. After send-keys, always sleep briefly (sleep 0.3) before capture-pane.
3. Use capture-pane with --new for incremental output.
4. Report observations at the end.
`)
	return sb.String()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
