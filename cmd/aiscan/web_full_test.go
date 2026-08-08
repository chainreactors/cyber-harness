//go:build full

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
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
	bus := eventbus.New[*aop.Event]()
	application := &runner.App{EventBus: bus}
	ingestor := &recordingArtifactIngestor{}

	wireWebApp(application, ingestor)
	extension, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Kind: toolpb.ArtifactKindService, CallId: "scan-1", Data: []byte(`{"ip":"127.0.0.1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	bus.Emit(&aop.Event{SessionId: "session-1", Payload: &aop.Event_Extension{Extension: extension}})
	if ingestor.artifact == nil || ingestor.artifact.CallId != "scan-1" || ingestor.artifact.Tool != "gogo" {
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
	artifact *toolpb.Artifact
}

func (i *recordingArtifactIngestor) IngestArtifact(_ context.Context, artifact *toolpb.Artifact) error {
	i.artifact = proto.CloneOf(artifact)
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
