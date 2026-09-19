package scan

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
)

type recordingPromptResolver struct {
	input prompt.Context
}

func (r *recordingPromptResolver) Build(_ context.Context, input prompt.Context) prompt.Result {
	r.input = input
	return prompt.Result{Prompt: "resolved worker prompt"}
}

func TestWorkerPromptUsesAgentConfigResolver(t *testing.T) {
	resolver := &recordingPromptResolver{}
	resolve := workerPrompt(VerifySystemTarget, "verify instructions", nil)
	result, err := resolve(t.Context(), &agent.Config{
		AgentName: "verifier", Model: "test-model", PromptResolver: resolver,
	})
	if err != nil || result != "resolved worker prompt" {
		t.Fatalf("worker prompt result = %q, %v", result, err)
	}
	if resolver.input.Target != VerifySystemTarget || resolver.input.Agent.Instructions != "verify instructions" {
		t.Fatalf("worker prompt input = %#v", resolver.input)
	}
	if resolver.input.Agent.Name != "verifier" || resolver.input.Agent.Model != "test-model" {
		t.Fatalf("worker agent context = %#v", resolver.input.Agent)
	}
}

func TestWorkerPromptRequiresPluginResolver(t *testing.T) {
	resolve := workerPrompt(SniperSystemTarget, "sniper instructions", nil)
	if result, err := resolve(t.Context(), &agent.Config{}); err == nil || result != "" {
		t.Fatalf("worker prompt without plugin = %q, %v", result, err)
	}
}
