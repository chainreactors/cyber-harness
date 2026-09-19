//go:build full

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	types "github.com/chainreactors/cyber/core/types"
	clientext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
)

func TestWebConfigStoreStagesBeforeAtomicCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	old := configForWebStore("old-model", "secret-key")
	oldBytes, err := cfg.MarshalDistributeConfigYAML(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, oldBytes, 0600); err != nil {
		t.Fatal(err)
	}

	store := &webConfigStore{explicit: path}
	incoming := configForWebStore("new-model", "")
	prepared, err := store.PrepareDistributeConfig(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DiscardDistributeConfig(prepared) })

	committedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(committedBytes) != string(oldBytes) {
		t.Fatal("PrepareDistributeConfig() changed the committed file")
	}
	if prepared.RuntimePath == "" || prepared.RuntimePath == path {
		t.Fatalf("runtime candidate path = %q", prepared.RuntimePath)
	}
	if filepath.Ext(prepared.RuntimePath) != ".yaml" {
		t.Fatalf("runtime candidate suffix = %q, want .yaml", prepared.RuntimePath)
	}
	var staged cfg.Option
	if err := cfg.LoadConfig(prepared.RuntimePath, &staged); err != nil {
		t.Fatalf("LoadConfig(%q): %v", prepared.RuntimePath, err)
	}
	if len(staged.Providers) == 0 || staged.Providers[0].Model != "new-model" {
		t.Fatalf("staged providers = %+v, want new-model", staged.Providers)
	}
	info, err := os.Stat(prepared.RuntimePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0600 {
		t.Fatalf("candidate permissions = %o, want 600", perm)
	}
	if got := cfg.ActiveLLMProvider(prepared.Config.GetLlm()).GetApiKey(); got != "secret-key" {
		t.Fatalf("prepared API key = %q, want preserved secret", got)
	}

	if err := store.CommitDistributeConfig(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	_, loaded, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	active := cfg.ActiveLLMProvider(committed.GetLlm())
	if !loaded || active.GetModel() != "new-model" || active.GetApiKey() != "secret-key" {
		t.Fatalf("committed config = %+v", committed.Llm)
	}
}

func TestEmbeddedAgentOptionUsesSameOriginIOA(t *testing.T) {
	base := &cfg.Option{Extensions: cfg.Values{clientext.ConfigKey: {"space": "case-1"}}}
	option, err := embeddedAgentOption(base, "promo-demo", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	if option.ServerURL != "http://promo-demo@127.0.0.1:18080" {
		t.Fatalf("server URL = %q", option.ServerURL)
	}
	if readClientOptions(t, &option).URL != "http://promo-demo@127.0.0.1:18080/ioa" {
		t.Fatalf("IOA URL = %q, want embedded same-origin endpoint", readClientOptions(t, &option).URL)
	}
	if option.NodeName != "local" || readClientOptions(t, &option).Space != "case-1" {
		t.Fatalf("embedded identity = name %q space %q", option.NodeName, readClientOptions(t, &option).Space)
	}
	if base.ServerURL != "" || readClientOptions(t, base).URL != "" || base.NodeName != "" {
		t.Fatalf("base option was mutated: %+v", base)
	}
}

func TestEmbeddedAgentOptionPreservesExplicitIOAAndNode(t *testing.T) {
	base := &cfg.Option{
		Extensions:  cfg.Values{clientext.ConfigKey: {"url": "http://ioa-token@127.0.0.1:18765"}},
		NodeOptions: cfg.NodeOptions{NodeName: "coordinator"},
	}
	option, err := embeddedAgentOption(base, "promo-demo", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	if readClientOptions(t, &option).URL != readClientOptions(t, base).URL || option.NodeName != "coordinator" {
		t.Fatalf("explicit IOA configuration was not preserved: %+v", option.Extensions)
	}
}

func configForWebStore(model, apiKey string) *types.DistributeConfig {
	return &types.DistributeConfig{
		Llm: &types.LLMConfig{
			ActiveProfile: "primary",
			Providers: []*types.LLMProviderConfig{{
				Id: "primary", Provider: "openai", Model: model, ApiKey: apiKey,
			}},
		},
	}
}
