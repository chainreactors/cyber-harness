package web

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestActivateLLMProfileReordersPrimaryProvider(t *testing.T) {
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
	if store.cfg.LLM.ActiveProfile != "fast" || store.cfg.LLM.Providers[0].ID != "fast" {
		t.Fatalf("active profile was not promoted: %+v", store.cfg.LLM)
	}
	if store.cfg.LLM.Model != "deepseek-fast" || store.cfg.LLM.APIKey != "key-2" {
		t.Fatalf("legacy primary fields not synchronized: %+v", store.cfg.LLM)
	}
	if status.LLM.ActiveProfile != "fast" || status.LLM.Model != "deepseek-fast" {
		t.Fatalf("status not synchronized: %+v", status.LLM)
	}
}
