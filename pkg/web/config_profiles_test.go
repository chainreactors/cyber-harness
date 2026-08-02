package web

import (
	"context"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

func TestActivateLLMProfileSelectsByID(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &configpb.DistributeConfig{Llm: &configpb.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*configpb.LLMProviderConfig{
			{Id: "primary", Name: "Primary", Provider: "openai", Model: "gpt-primary", ApiKey: "key-1"},
			{Id: "fast", Name: "Fast", Provider: "openai", Model: "deepseek-fast", ApiKey: "key-2"},
		},
	}}
	service := NewService(ServiceConfig{ConfigStore: store})

	status, err := service.ActivateLLMProfile(context.Background(), "fast")
	if err != nil {
		t.Fatal(err)
	}
	// Selection is by id: the list order is untouched and Active() resolves
	// the chosen profile.
	if store.cfg.Llm.ActiveProfile != "fast" || store.cfg.Llm.Providers[0].Id != "primary" {
		t.Fatalf("active profile not switched by id: %+v", store.cfg.Llm)
	}
	if active := cfg.ActiveLLMProvider(store.cfg.Llm); active.Provider != "openai" || active.Model != "deepseek-fast" || active.ApiKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.GetLlm().GetActiveProfile() != "fast" || status.GetLlm().GetActive().GetProvider() != "openai" || status.GetLlm().GetActive().GetModel() != "deepseek-fast" {
		t.Fatalf("view not synchronized: %+v", status.GetLlm())
	}
}

func TestConfigStatusIncludesModelLimits(t *testing.T) {
	conf := &configpb.DistributeConfig{Llm: &configpb.LLMConfig{
		ActiveProfile: "large",
		Providers: []*configpb.LLMProviderConfig{{
			Id: "large", Provider: "anthropic", Model: "glm-5.2[1m]",
			MaxTokens: 32768, ContextWindow: 1000000,
		}},
	}}
	view := ConfigViewFromDistribute(conf, "aiscan.yaml", true)
	if view.GetLlm().GetActive().GetMaxTokens() != 32768 || view.GetLlm().GetActive().GetContextWindow() != 1000000 {
		t.Fatalf("active limits missing from view: %+v", view.GetLlm())
	}
	if len(view.GetLlm().GetProviders()) != 1 || view.GetLlm().GetProviders()[0].GetMaxTokens() != 32768 || view.GetLlm().GetProviders()[0].GetContextWindow() != 1000000 {
		t.Fatalf("profile limits missing from view: %+v", view.GetLlm().GetProviders())
	}
}

func TestSaveConfigRejectsNegativeModelLimits(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	for _, mutate := range []func(*configpb.LLMProviderConfig){
		func(p *configpb.LLMProviderConfig) { p.MaxTokens = -1 },
		func(p *configpb.LLMProviderConfig) { p.ContextWindow = -1 },
	} {
		profile := &configpb.LLMProviderConfig{Id: "bad", Model: "test-model"}
		mutate(profile)
		conf := &configpb.DistributeConfig{Llm: &configpb.LLMConfig{
			Providers: []*configpb.LLMProviderConfig{profile},
		}}
		if _, err := service.SaveConfig(context.Background(), conf); err == nil {
			t.Fatal("SaveConfig() accepted a negative model limit")
		}
		if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
			t.Fatal("invalid config was persisted")
		}
	}
}

func TestSaveConfigRejectsEmptyProfileModel(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	conf := &configpb.DistributeConfig{Llm: &configpb.LLMConfig{
		Providers: []*configpb.LLMProviderConfig{{Id: "empty", Name: "Empty", Model: "  "}},
	}}

	if _, err := service.SaveConfig(context.Background(), conf); err == nil {
		t.Fatal("SaveConfig() accepted an empty profile model")
	}
	if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
		t.Fatal("invalid config was persisted")
	}
}

func TestActivateLLMProfileRejectsEmptyModel(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &configpb.DistributeConfig{Llm: &configpb.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*configpb.LLMProviderConfig{
			{Id: "primary", Model: "gpt-primary"},
			{Id: "empty", Model: ""},
		},
	}}
	service := NewService(ServiceConfig{ConfigStore: store})

	if _, err := service.ActivateLLMProfile(context.Background(), "empty"); err == nil {
		t.Fatal("ActivateLLMProfile() accepted an empty model")
	}
	if store.cfg.Llm.ActiveProfile != "primary" {
		t.Fatalf("active profile = %q, want primary", store.cfg.Llm.ActiveProfile)
	}
}
