package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/skills"
)

func TestAgentAutomaticWorkflowUsesScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}

	scanOutput := "[scan.summary] completed inputs 1 services 1"

	dir := t.TempDir()
	stubScript := filepath.Join(dir, "aiscan-stub")
	scriptContent := "#!/bin/sh\nprintf '" + strings.ReplaceAll(scanOutput, "'", "'\\''") + "'\n"
	os.WriteFile(stubScript, []byte(scriptContent), 0o755)

	registry := command.NewRegistry()
	registry.Register(&stubPseudoCommand{name: "scan"}, "")

	bash := command.NewBashTool(dir, 5, registry)
	bash.SetSelfBinary(stubScript)
	registry.RegisterTool(bash)

	tmux := command.NewTmuxCommand(bash.Manager(), registry)
	tmux.SetSelfBin(stubScript)
	registry.Register(tmux, "core")

	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: provider.FunctionCall{
							Name:      "bash",
							Arguments: scannerBashArgs("scan -i 127.0.0.1 --mode quick"),
						},
					},
				},
			}),
			chatResponse(provider.NewTextMessage("assistant", "final report")),
		},
	}

	systemPrompt := BuildSystemPrompt(&PromptConfig{
		Tools:       registry,
		ScannerDocs: registry.UsageDocs(),
	})

	result, err := (Config{
		Provider:     llm,
		Tools:        registry,
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	}).Run(context.Background(), "scan 127.0.0.1")
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

type stubPseudoCommand struct{ name string }

func (c *stubPseudoCommand) Name() string                                         { return c.name }
func (c *stubPseudoCommand) Usage() string                                        { return c.name }
func (c *stubPseudoCommand) Execute(_ context.Context, _ []string) (string, error) { return "", nil }

func TestAgentPromptIncludesEmbeddedSkillIndexAndExpansion(t *testing.T) {
	registry := command.NewRegistry()
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	registry.RegisterTool(command.NewReadTool(t.TempDir(), store))

	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "done")),
		},
	}
	systemPrompt := BuildSystemPrompt(&PromptConfig{
		Tools:  registry,
		Skills: store.Skills,
	})
	task := skills.ExpandCommand("/skill:scan scan 127.0.0.1", store)

	result, err := (Config{
		Provider:     llm,
		Tools:        registry,
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	}).Run(context.Background(), task)
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

func scannerBashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}
