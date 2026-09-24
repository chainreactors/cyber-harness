package api

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
)

func TestLLMHealthUsesEffectiveRuntime(t *testing.T) {
	var gotAuth string
	server := stubLLMServer(t, "runtime-pong", &gotAuth)
	defer server.Close()
	store := &fakeConfigStore{cfg: &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "saved", Provider: "anthropic", Model: "saved-model", ApiKey: "stored-key", BaseUrl: "http://127.0.0.1:1"}},
	}}}
	api := NewConfig(store, ConfigOptions{RuntimeLLM: func() provider.ProviderConfig {
		return provider.ProviderConfig{Provider: "openai", Model: "runtime-model", APIKey: "runtime-key", BaseURL: server.URL + "/v1"}
	}})
	for _, request := range []*types.LLMProbeRequest{nil, {}} {
		result, err := api.TestLLM(context.Background(), request)
		if err != nil || !result.GetOk() || result.GetModel() != "runtime-model" || result.GetReply() != "runtime-pong" || gotAuth != "Bearer runtime-key" {
			t.Fatalf("runtime probe = %v, error = %v, auth = %q", result, err, gotAuth)
		}
	}
	// Editing a saved profile still tests that profile, without runtime overrides.
	result, err := api.TestLLM(context.Background(), &types.LLMProbeRequest{ProfileId: "saved", Provider: "openai", Model: "edited-model", BaseUrl: server.URL + "/v1"})
	if err != nil || !result.GetOk() || result.GetModel() != "edited-model" || gotAuth != "Bearer stored-key" {
		t.Fatalf("settings probe = %v, error = %v, auth = %q", result, err, gotAuth)
	}
	api.options.RuntimeLLM = func() provider.ProviderConfig {
		return provider.ProviderConfig{Provider: "openai", Model: "cleared-key", BaseURL: server.URL + "/v1"}
	}
	result, err = api.TestLLM(context.Background(), &types.LLMProbeRequest{})
	if err != nil || result.GetOk() || !strings.Contains(result.GetError(), "API key") {
		t.Fatalf("empty runtime key must not fall back to a stored key: %v, %v", result, err)
	}
}
