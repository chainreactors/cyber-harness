//go:build full

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/web"
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
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0600 {
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

func TestWireWebAppBindsSCONodesForReloadedApp(t *testing.T) {
	store, err := web.NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	application := &runner.App{SCOSidecar: &output.SCOSidecar{}}

	wireWebApp(application, store)
	if application.SCOSidecar.OnNodes == nil {
		t.Fatal("reloaded app SCO sidecar callback was not bound")
	}
	application.SCOSidecar.OnNodes("scan-1", []json.RawMessage{
		json.RawMessage(`{"cstx_id":"node-1","cstx_type":"asset","data":{}}`),
	})
	nodes, err := store.ListSCONodesByScanID(context.Background(), "scan-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("persisted SCO nodes = %d, want 1", len(nodes))
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
