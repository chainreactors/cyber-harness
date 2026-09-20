package scanner

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
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

func TestWorkerPromptUsesAgentConfigResolver(t *testing.T) {
	resolver := &recordingPromptResolver{}
	resolve := workerPrompt(scan.VerifySystemTarget, "verify instructions", nil)
	result, err := resolve(t.Context(), &agent.Config{
		AgentName: "verifier", Model: "test-model", PromptResolver: resolver,
	})
	if err != nil || result != "resolved worker prompt" {
		t.Fatalf("worker prompt result = %q, %v", result, err)
	}
	if resolver.input.Target != scan.VerifySystemTarget || resolver.input.Agent.Instructions != "verify instructions" {
		t.Fatalf("worker prompt input = %#v", resolver.input)
	}
	if resolver.input.Agent.Name != "verifier" || resolver.input.Agent.Model != "test-model" {
		t.Fatalf("worker agent context = %#v", resolver.input.Agent)
	}
}

func TestWorkerPromptRequiresPluginResolver(t *testing.T) {
	resolve := workerPrompt(scan.SniperSystemTarget, "sniper instructions", nil)
	if result, err := resolve(t.Context(), &agent.Config{}); err == nil || result != "" {
		t.Fatalf("worker prompt without plugin = %q, %v", result, err)
	}
}
