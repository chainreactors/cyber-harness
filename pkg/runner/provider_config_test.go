package runner

import (
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
)

func TestProviderConfigSelectsActiveProfileAndFallbacks(t *testing.T) {
	option := cfg.Option{LLMOptions: cfg.LLMOptions{
		ActiveProfile: "openai",
		Providers: []cfg.LLMProviderEntry{
			{ID: "deepseek", Provider: "deepseek", APIKey: "dk-111", Model: "deepseek-chat", MaxTokens: 8192},
			{ID: "openai", Provider: "openai", APIKey: "sk-222", Model: "gpt-4o", MaxTokens: 32768},
		},
	}}
	primary := ProviderConfig(&option)
	if primary.Provider != "openai" || primary.APIKey != "sk-222" || primary.MaxTokens != 32768 {
		t.Fatalf("primary profile = %+v", primary)
	}
	fallbacks := FallbackProviderConfigs(&option)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "deepseek" || fallbacks[0].APIKey != "dk-111" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}

func TestProviderConfigExplicitFieldsWin(t *testing.T) {
	option := cfg.Option{LLMOptions: cfg.LLMOptions{
		Provider: "anthropic", APIKey: "cli-key", Model: "cli-model",
		Providers: []cfg.LLMProviderEntry{{Provider: "deepseek", APIKey: "fallback-key", Model: "deepseek-chat"}},
	}}
	primary := ProviderConfig(&option)
	if primary.Provider != "anthropic" || primary.APIKey != "cli-key" || primary.Model != "cli-model" {
		t.Fatalf("explicit provider = %+v", primary)
	}
	if fallbacks := FallbackProviderConfigs(&option); len(fallbacks) != 1 || fallbacks[0].Provider != "deepseek" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}
