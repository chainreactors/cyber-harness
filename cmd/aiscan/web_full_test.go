//go:build full

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func TestWebConfigStoreStagesBeforeAtomicCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aiscan.yaml")
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

func TestWireWebAppBindsRawArtifactsForReloadedApp(t *testing.T) {
	bus := eventbus.New[output.ToolDataEvent]()
	application := &runner.App{Artifacts: output.NewArtifactStream(bus)}
	defer application.Artifacts.Close()
	ingestor := &recordingArtifactIngestor{}

	wireWebApp(application, ingestor)
	bus.Emit(output.ToolDataEvent{
		Tool: "gogo", Kind: output.ToolDataService, CallID: "scan-1",
		Data: map[string]string{"ip": "127.0.0.1"},
	})
	if ingestor.operationID != "scan-1" || ingestor.artifact.Tool != "gogo" {
		t.Fatalf("artifact was not forwarded: %+v", ingestor.artifact)
	}
}

type recordingArtifactIngestor struct {
	operationID string
	artifact    output.ToolArtifact
}

func (i *recordingArtifactIngestor) IngestArtifact(_ context.Context, operationID string, artifact output.ToolArtifact) error {
	i.operationID, i.artifact = operationID, artifact
	return nil
}

func (*recordingArtifactIngestor) NormalizeArtifact(context.Context, string, string, []byte) (uint64, uint64, error) {
	return 0, 0, nil
}

func (*recordingArtifactIngestor) SupportedArtifacts() []string { return nil }
func (*recordingArtifactIngestor) Close() error                 { return nil }

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
