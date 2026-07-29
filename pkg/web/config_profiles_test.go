package web

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestActivateLLMProfileSelectsByID(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg.LLM.ActiveProfile = "primary"
	store.cfg.LLM.Providers = []webproto.LLMProviderConfig{
		{ID: "primary", Name: "Primary", Provider: "openai", Model: "gpt-primary", APIKey: "key-1"},
		{ID: "fast", Name: "Fast", Provider: "deepseek", Model: "deepseek-fast", APIKey: "key-2"},
	}
	service := NewService(ServiceConfig{ConfigStore: store})

	status, err := service.ActivateLLMProfile(context.Background(), "fast")
	if err != nil {
		t.Fatal(err)
	}
	// Selection is by id: the list order is untouched and Active() resolves
	// the chosen profile.
	if store.cfg.LLM.ActiveProfile != "fast" || store.cfg.LLM.Providers[0].ID != "primary" {
		t.Fatalf("active profile not switched by id: %+v", store.cfg.LLM)
	}
	if active := store.cfg.LLM.Active(); active.Provider != "openai" || active.Model != "deepseek-fast" || active.APIKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.LLM.ActiveProfile != "fast" || status.LLM.Provider != "openai" || status.LLM.Model != "deepseek-fast" {
		t.Fatalf("status not synchronized: %+v", status.LLM)
	}
}

func TestConfigStatusIncludesModelLimits(t *testing.T) {
	var cfg webproto.DistributeConfig
	cfg.LLM.ActiveProfile = "large"
	cfg.LLM.Providers = []webproto.LLMProviderConfig{{
		ID: "large", Provider: "anthropic", Model: "glm-5.2[1m]",
		MaxTokens: 32768, ContextWindow: 1000000,
	}}
	status := ConfigStatusFromDistribute(&cfg, "aiscan.yaml", true)
	if status.LLM.MaxTokens != 32768 || status.LLM.ContextWindow != 1000000 {
		t.Fatalf("active limits missing from status: %+v", status.LLM)
	}
	if len(status.LLM.Profiles) != 1 || status.LLM.Profiles[0].MaxTokens != 32768 || status.LLM.Profiles[0].ContextWindow != 1000000 {
		t.Fatalf("profile limits missing from status: %+v", status.LLM.Profiles)
	}
}

func TestSaveConfigRejectsNegativeModelLimits(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	for _, mutate := range []func(*webproto.LLMProviderConfig){
		func(p *webproto.LLMProviderConfig) { p.MaxTokens = -1 },
		func(p *webproto.LLMProviderConfig) { p.ContextWindow = -1 },
	} {
		var cfg webproto.DistributeConfig
		profile := webproto.LLMProviderConfig{ID: "bad", Model: "test-model"}
		mutate(&profile)
		cfg.LLM.Providers = []webproto.LLMProviderConfig{profile}
		if _, err := service.SaveConfig(context.Background(), cfg); err == nil {
			t.Fatal("SaveConfig() accepted a negative model limit")
		}
		if len(store.cfg.LLM.Providers) != 0 {
			t.Fatal("invalid config was persisted")
		}
	}
}

func TestSaveConfigRejectsEmptyProfileModel(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	var cfg webproto.DistributeConfig
	cfg.LLM.Providers = []webproto.LLMProviderConfig{{ID: "empty", Name: "Empty", Model: "  "}}

	if _, err := service.SaveConfig(context.Background(), cfg); err == nil {
		t.Fatal("SaveConfig() accepted an empty profile model")
	}
	if len(store.cfg.LLM.Providers) != 0 {
		t.Fatal("invalid config was persisted")
	}
}

func TestActivateLLMProfileRejectsEmptyModel(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg.LLM.ActiveProfile = "primary"
	store.cfg.LLM.Providers = []webproto.LLMProviderConfig{
		{ID: "primary", Model: "gpt-primary"},
		{ID: "empty", Model: ""},
	}
	service := NewService(ServiceConfig{ConfigStore: store})

	if _, err := service.ActivateLLMProfile(context.Background(), "empty"); err == nil {
		t.Fatal("ActivateLLMProfile() accepted an empty model")
	}
	if store.cfg.LLM.ActiveProfile != "primary" {
		t.Fatalf("active profile = %q, want primary", store.cfg.LLM.ActiveProfile)
	}
}
