package config

import (
	"testing"

	types "github.com/chainreactors/cyber/core/types"
)

func TestProviderConfigSelectsActiveProfileAndFallbacks(t *testing.T) {
	option := Option{LLMOptions: LLMOptions{
		ActiveProfile: "openai",
		Providers: []LLMProviderEntry{
			{ID: "deepseek", Provider: "openai", APIKey: "dk-111", Model: "deepseek-chat", MaxTokens: 8192},
			{ID: "openai", Provider: "openai", APIKey: "sk-222", Model: "gpt-4o", MaxTokens: 32768},
		},
	}}
	primary := ProviderConfig(&option)
	if primary.Provider != "openai" || primary.APIKey != "sk-222" || primary.MaxTokens != 32768 {
		t.Fatalf("primary profile = %+v", primary)
	}
	fallbacks := FallbackProviderConfigs(&option)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "openai" || fallbacks[0].APIKey != "dk-111" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}

func TestProviderConfigExplicitFieldsWin(t *testing.T) {
	option := Option{LLMOptions: LLMOptions{
		Provider: "anthropic", APIKey: "cli-key", Model: "cli-model",
		Providers: []LLMProviderEntry{{Provider: "openai", APIKey: "fallback-key", Model: "deepseek-chat"}},
	}}
	primary := ProviderConfig(&option)
	if primary.Provider != "anthropic" || primary.APIKey != "cli-key" || primary.Model != "cli-model" {
		t.Fatalf("explicit provider = %+v", primary)
	}
	if fallbacks := FallbackProviderConfigs(&option); len(fallbacks) != 1 || fallbacks[0].Provider != "openai" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}

func TestSelectedProfileOverrideDoesNotRepeatActiveFallback(t *testing.T) {
	option := Option{LLMOptions: LLMOptions{
		ActiveProfile: "primary", Model: "override",
		Providers: []LLMProviderEntry{
			{ID: "primary", Provider: "openai", Model: "original"},
			{ID: "secondary", Provider: "openai", Model: "backup"},
		},
	}}
	if ProviderConfig(&option).Model != "override" {
		t.Fatal("selected profile ignored field override")
	}
	fallbacks := FallbackProviderConfigs(&option)
	if len(fallbacks) != 1 || fallbacks[0].Model != "backup" {
		t.Fatalf("active profile repeated in fallbacks: %+v", fallbacks)
	}
}

func TestProviderConfigFromProtoSelectsActiveProfileAndFallbacks(t *testing.T) {
	llm := &types.LLMConfig{
		ActiveProfile: "openai",
		Providers: []*types.LLMProviderConfig{
			{Id: "deepseek", Provider: "openai", ApiKey: "dk-111", Model: "deepseek-chat", MaxTokens: 8192},
			{Id: "openai", Provider: "openai", ApiKey: "sk-222", Model: "gpt-4o", MaxTokens: 32768},
		},
	}
	primary := ProviderConfigFromProto(llm)
	if primary.Provider != "openai" || primary.APIKey != "sk-222" || primary.MaxTokens != 32768 {
		t.Fatalf("primary profile = %+v", primary)
	}
	fallbacks := FallbackProviderConfigsFromProto(llm)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "openai" || fallbacks[0].APIKey != "dk-111" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}

func TestProviderConfigFromProtoInfersProtocolFromBaseURL(t *testing.T) {
	llm := &types.LLMConfig{Providers: []*types.LLMProviderConfig{
		{Id: "claude", BaseUrl: "https://api.anthropic.com", ApiKey: "ak", Model: "claude-opus-4-7"},
	}}
	primary := ProviderConfigFromProto(llm)
	if primary.Provider != "anthropic" {
		t.Fatalf("inferred provider = %q, want anthropic", primary.Provider)
	}
}
