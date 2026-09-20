package scanner

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/tools/scan"
)

type recordingPromptResolver struct {
	input prompt.Context
}

type workerProvider struct {
	requests []*agent.ChatCompletionRequest
}

func (*workerProvider) Name() string { return "worker-test" }
func (p *workerProvider) ChatCompletion(_ context.Context, request *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	p.requests = append(p.requests, request)
	return &agent.ChatCompletionResponse{Choices: []agent.Choice{{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text("status:confirmed")}}}}}, nil
}

type workerPromptResolver struct{ inputs []prompt.Context }

func (r *workerPromptResolver) Build(_ context.Context, input prompt.Context) prompt.Result {
	r.inputs = append(r.inputs, input)
	return prompt.Result{Prompt: string(input.Target)}
}

func (r *recordingPromptResolver) Build(_ context.Context, input prompt.Context) prompt.Result {
	r.input = input
	return prompt.Result{Prompt: "resolved worker prompt"}
}

func TestScannerPreparationUsesAgentConfigResolver(t *testing.T) {
	resolver := &recordingPromptResolver{}
	worker := scannerSubagents(func(string) string { return "verify instructions" })[0]
	cfg, task, err := worker.Prepare(t.Context(), agent.Config{
		AgentName: "verifier", Model: "test-model", PromptResolver: resolver,
	}, subagent.Input{Prompt: "verify task"})
	if err != nil || task != "verify task" {
		t.Fatalf("prepared task = %q, %v", task, err)
	}
	if cfg.SystemPrompt != "resolved worker prompt" || cfg.SystemPromptFn != nil {
		t.Fatalf("system prompt was not frozen: %+v", cfg)
	}
	if resolver.input.Target != scan.VerifySystemTarget || resolver.input.Agent.Instructions != "verify instructions" {
		t.Fatalf("worker prompt input = %#v", resolver.input)
	}
	if resolver.input.Agent.Name != "verifier" || resolver.input.Agent.Model != "test-model" {
		t.Fatalf("worker agent context = %#v", resolver.input.Agent)
	}
}

func TestScannerPreparationRequiresPluginResolver(t *testing.T) {
	worker := scannerSubagents(func(string) string { return "sniper instructions" })[1]
	if _, result, err := worker.Prepare(t.Context(), agent.Config{}, subagent.Input{Prompt: "research task"}); err == nil || result != "" {
		t.Fatalf("worker prompt without plugin = %q, %v", result, err)
	}
}
