//go:build full

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
	"gopkg.in/yaml.v3"
)

func TestWebConfigStoreStagesBeforeAtomicCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aiscan.yaml")
	old := configForWebStore("old-model", "secret-key")
	oldBytes, err := yaml.Marshal(&old)
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
	info, err := os.Stat(prepared.RuntimePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("candidate permissions = %o, want 600", perm)
	}
	if got := prepared.Config.LLM.Active().APIKey; got != "secret-key" {
		t.Fatalf("prepared API key = %q, want preserved secret", got)
	}

	if err := store.CommitDistributeConfig(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	_, loaded, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !loaded || committed.LLM.Active().Model != "new-model" || committed.LLM.Active().APIKey != "secret-key" {
		t.Fatalf("committed config = %+v", committed.LLM)
	}
}

func configForWebStore(model, apiKey string) webproto.DistributeConfig {
	var cfg webproto.DistributeConfig
	cfg.LLM.ActiveProfile = "primary"
	cfg.LLM.Providers = []webproto.LLMProviderConfig{{
		ID: "primary", Provider: "openai", Model: model, APIKey: apiKey,
	}}
	return cfg
}
