//go:build full

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	operationpb "github.com/chainreactors/aiscan/aop/operation"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	cfg "github.com/chainreactors/aiscan/core/config"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/anypb"
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

func TestWireWebAppBindsRawArtifactsForReloadedApp(t *testing.T) {
	application := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.Dependencies{})
	ingestor := &recordingArtifactIngestor{}

	wireWebApp(application.App, ingestor)
	event := &aop.Event{SessionId: "session-1"}
	extension, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Kind: toolpb.ArtifactKindService, Data: []byte(`{"ip":"127.0.0.1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	event.Payload = &aop.Event_Extension{Extension: extension}
	if err := aop.SetTypedExtension(event, &operationpb.Ref{
		CallId: "scan-1", Correlation: operationpb.Correlation_CORRELATION_EXPLICIT,
	}); err != nil {
		t.Fatal(err)
	}
	application.App.Emit(event)
	if ingestor.operationID != "scan-1" || ingestor.artifact == nil || ingestor.artifact.Tool != "gogo" {
		t.Fatalf("artifact was not forwarded: %+v", ingestor.artifact)
	}
}

func TestEmbeddedAgentOptionUsesSameOriginIOA(t *testing.T) {
	base := &cfg.Option{IOAOptions: cfg.IOAOptions{Space: "case-1"}}
	option, err := embeddedAgentOption(base, "promo-demo", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	if option.ServerURL != "http://promo-demo@127.0.0.1:18080" {
		t.Fatalf("server URL = %q", option.ServerURL)
	}
	if option.IOAURL != "http://promo-demo@127.0.0.1:18080/ioa" {
		t.Fatalf("IOA URL = %q, want embedded same-origin endpoint", option.IOAURL)
	}
	if option.IOANodeName != "local" || option.Space != "case-1" {
		t.Fatalf("embedded identity = name %q space %q", option.IOANodeName, option.Space)
	}
	if base.ServerURL != "" || base.IOAURL != "" || base.IOANodeName != "" {
		t.Fatalf("base option was mutated: %+v", base)
	}
}

func TestEmbeddedAgentOptionPreservesExplicitIOAAndNode(t *testing.T) {
	base := &cfg.Option{
		IOAOptions: cfg.IOAOptions{
			IOAURL:      "http://ioa-token@127.0.0.1:18765",
			IOANodeName: "coordinator",
		},
	}
	option, err := embeddedAgentOption(base, "promo-demo", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	if option.IOAURL != base.IOAURL || option.IOANodeName != "coordinator" {
		t.Fatalf("explicit IOA configuration was not preserved: %+v", option.IOAOptions)
	}
}

type recordingArtifactIngestor struct {
	operationID string
	artifact    *toolpb.Artifact
}

func (i *recordingArtifactIngestor) NormalizeArtifact(_ context.Context, operationID, tool string, data []byte) (uint64, uint64, error) {
	i.operationID = operationID
	i.artifact = &toolpb.Artifact{Tool: tool, Data: append([]byte(nil), data...)}
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
