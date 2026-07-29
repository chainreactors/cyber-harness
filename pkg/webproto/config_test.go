package webproto

import "testing"

func TestMigrateLLMConfigNormalizesProviderProtocol(t *testing.T) {
	config := LLMConfig{Providers: []LLMProviderConfig{
		{ID: "deepseek", Provider: "deepseek", BaseURL: "https://api.deepseek.com/v1"},
		{ID: "claude", Provider: "anthropic"},
	}}
	MigrateLLMConfig(&config, LLMProviderConfig{})

	if config.Providers[0].Provider != "openai" {
		t.Fatalf("OpenAI-compatible provider = %q", config.Providers[0].Provider)
	}
	if config.Providers[1].Provider != "anthropic" {
		t.Fatalf("Anthropic provider = %q", config.Providers[1].Provider)
	}
}
