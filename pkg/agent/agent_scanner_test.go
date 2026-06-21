package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

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

type stubPseudoCommand struct {
	name   string
	output string
}

func (c *stubPseudoCommand) Name() string  { return c.name }
func (c *stubPseudoCommand) Usage() string { return c.name }
func (c *stubPseudoCommand) Execute(_ context.Context, _ []string) error {
	fmt.Fprint(commands.Output, c.output)
	return nil
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

func scannerBashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
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
