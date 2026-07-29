package webproto

import "testing"

func TestMigrateLLMConfigCanonicalizesProviderProtocol(t *testing.T) {
	config := LLMConfig{Providers: []LLMProviderConfig{
		{ID: "openai", Provider: " OPENAI ", BaseURL: "https://api.deepseek.com/v1"},
		{ID: "claude", Provider: "ANTHROPIC"},
		{ID: "invalid", Provider: "deepseek"},
	}}
	MigrateLLMConfig(&config, LLMProviderConfig{})

	if config.Providers[0].Provider != "openai" {
		t.Fatalf("OpenAI-compatible provider = %q", config.Providers[0].Provider)
	}
	if config.Providers[1].Provider != "anthropic" {
		t.Fatalf("Anthropic provider = %q", config.Providers[1].Provider)
	}
	if config.Providers[2].Provider != "deepseek" {
		t.Fatalf("unsupported provider must not be rewritten, got %q", config.Providers[2].Provider)
	}
}
