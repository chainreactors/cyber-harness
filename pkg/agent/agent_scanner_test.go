package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/skills"
)

func TestAgentAutomaticWorkflowUsesScan(t *testing.T) {
	registry := command.NewRegistry()
	scan := newScannerRecording("scan")
	scan.output = "[scan.summary] completed inputs 1 services 1 web 1 probes 1 fp 0 weakpass 0 vulns 1 verified 0 errors 0 1s\n[neutron_poc] http://127.0.0.1 \"template=test-cve severity=high\""
	registry.Register(scan, "")
	registry.Register(newScannerRecording("gogo"), "")
	registry.Register(newScannerRecording("spray"), "")
	registry.Register(newScannerRecording("zombie"), "")
	registry.Register(newScannerRecording("neutron"), "")

	bash := command.NewBashTool(t.TempDir(), 5, registry)
	registry.RegisterTool(bash)

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
			chatResponse(provider.NewTextMessage("assistant", "final report: one high severity vulnerability")),
		},
	}

	systemPrompt := BuildSystemPrompt(&PromptConfig{
		Tools:       registry,
		ScannerDocs: registry.UsageDocs(),
	})
	a := New(llm, registry, Config{
		SystemPrompt: systemPrompt,
		Model:        "test-model",
	})

	result, err := a.Prompt(context.Background(), "scan 127.0.0.1 and summarize findings")
	if err != nil {
		t.Fatalf("agent.Prompt() error = %v", err)
	}
	if result.Output != "final report: one high severity vulnerability" {
		t.Fatalf("result = %q", result.Output)
	}
	if got := scan.lastArgs(); !reflect.DeepEqual(got, []string{"-i", "127.0.0.1", "--mode", "quick"}) {
		t.Fatalf("scan args = %#v", got)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Function.Name != "bash" {
		t.Fatalf("first provider request tools = %#v", requests[0].Tools)
	}
	if !hasToolMessage(requests[1].Messages, "call-1", "[scan.summary] completed") {
		t.Fatalf("second provider request did not include scan tool result: %#v", requests[1].Messages)
	}
}

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
		t.Fatalf("agent.Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("result = %q", result.Output)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) < 2 {
		t.Fatalf("messages = %#v", requests[0].Messages)
	}
	system := requests[0].Messages[0]
	if system.Role != "system" || system.Content == nil || !strings.Contains(*system.Content, "<available_skills>") {
		t.Fatalf("system prompt missing skills: %#v", system)
	}
	user := requests[0].Messages[1]
	if user.Role != "user" || user.Content == nil || !strings.Contains(*user.Content, `<skill name="scan"`) || !strings.Contains(*user.Content, "scan 127.0.0.1") {
		t.Fatalf("user prompt missing expanded skill: %#v", user)
	}
}

type scannerRecording struct {
	name   string
	output string

	mu   sync.Mutex
	args [][]string
}

func newScannerRecording(name string) *scannerRecording {
	return &scannerRecording{name: name}
}

func (c *scannerRecording) Name() string { return c.name }

func (c *scannerRecording) Usage() string {
	return fmt.Sprintf("%s - test command\nUsage: %s [options]", c.name, c.name)
}

func (c *scannerRecording) Execute(_ context.Context, args []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := append([]string(nil), args...)
	c.args = append(c.args, copied)
	if c.output != "" {
		return c.output, nil
	}
	return fmt.Sprintf("[%s] ok args=%s", c.name, strings.Join(args, " ")), nil
}

func (c *scannerRecording) lastArgs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.args) == 0 {
		return nil
	}
	return append([]string(nil), c.args[len(c.args)-1]...)
}

func scannerBashArgs(cmd string) string {
	data, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		panic(err)
	}
	return string(data)
}
