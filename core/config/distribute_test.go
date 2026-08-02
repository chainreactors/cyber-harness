package config

import (
	"testing"

	types "github.com/chainreactors/aiscan/pkg/types"
)

func TestNormalizeLLMConfigCanonicalizesProviderProtocol(t *testing.T) {
	llm := &types.LLMConfig{Providers: []*types.LLMProviderConfig{
		{Id: "openai", Provider: " OPENAI ", BaseUrl: "https://api.deepseek.com/v1"},
		{Id: "claude", Provider: "ANTHROPIC"},
		{Id: "invalid", Provider: "deepseek"},
	}}
	NormalizeLLMConfig(llm)

	if llm.Providers[0].Provider != "openai" {
		t.Fatalf("OpenAI-compatible provider = %q", llm.Providers[0].Provider)
	}
	if llm.Providers[1].Provider != "anthropic" {
		t.Fatalf("Anthropic provider = %q", llm.Providers[1].Provider)
	}
	if llm.Providers[2].Provider != "deepseek" {
		t.Fatalf("unsupported provider must not be rewritten, got %q", llm.Providers[2].Provider)
	}
}

func TestDistributeConfigYAMLRoundTripPreservesProviderCapabilities(t *testing.T) {
	images := false
	want := &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{{
			Id: "primary", Provider: "openai", Model: "gpt-test",
			Timeout: 45, Images: &images,
		}},
	}}
	data, err := MarshalDistributeConfigYAML(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadDistributeConfigYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	profile := got.GetLlm().GetProviders()[0]
	if profile.GetTimeout() != 45 || profile.Images == nil || profile.GetImages() {
		t.Fatalf("provider capabilities lost: %+v", profile)
	}
}
