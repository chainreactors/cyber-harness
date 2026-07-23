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
	if active := store.cfg.LLM.Active(); active.Model != "deepseek-fast" || active.APIKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.LLM.ActiveProfile != "fast" || status.LLM.Model != "deepseek-fast" {
		t.Fatalf("status not synchronized: %+v", status.LLM)
	}
}
