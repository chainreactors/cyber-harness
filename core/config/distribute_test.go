package config

import (
	"testing"

	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

func TestNormalizeLLMConfigCanonicalizesProviderProtocol(t *testing.T) {
	llm := &configpb.LLMConfig{Providers: []*configpb.LLMProviderConfig{
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
