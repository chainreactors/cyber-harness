//go:build full

package main

import (
	"context"
	clientext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	cfg "github.com/chainreactors/cyber/core/config"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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

func TestArtifactProjectionOwnsRawArtifactObservation(t *testing.T) {
	events := coreevents.New()
	ingestor := &recordingArtifactImporter{}
	projection, err := newArtifactProjection(ingestor, telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	event := &aop.Event{SessionId: "session-1"}
	encoded, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Kind: toolpb.ArtifactKindService, Data: []byte(`{"ip":"127.0.0.1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	event.Payload = &aop.Event_Extension{Extension: encoded}
	if err := aop.SetTypedExtension(event, &operationpb.Ref{
		CallId: "scan-1", Correlation: operationpb.Correlation_CORRELATION_EXPLICIT,
	}); err != nil {
		t.Fatal(err)
	}
	events.Publish(event)
	if err := projection.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ingestor.operationID != "scan-1" || ingestor.artifact == nil || ingestor.artifact.Tool != "gogo" {
		t.Fatalf("artifact was not forwarded: %+v", ingestor.artifact)
	}
}

func TestArtifactProjectionDoesNotBlockAOPPublisher(t *testing.T) {
	events := coreevents.New()
	importer := &blockingArtifactImporter{started: make(chan struct{}), release: make(chan struct{})}
	projection, err := newArtifactProjection(importer, telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	encoded, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Data: []byte(`{"ip":"127.0.0.1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan struct{})
	go func() {
		events.Publish(&aop.Event{Payload: &aop.Event_Extension{Extension: encoded}})
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("artifact importer blocked AOP publication")
	}
	select {
	case <-importer.started:
	case <-time.After(time.Second):
		t.Fatal("artifact consumer did not start")
	}
	close(importer.release)
	if err := projection.Flush(t.Context()); err != nil {
		t.Fatal(err)
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

type recordingArtifactImporter struct {
	operationID string
	artifact    *toolpb.Artifact
}

func (i *recordingArtifactImporter) ImportArtifact(_ context.Context, operationID string, artifact *toolpb.Artifact) (uint64, uint64, error) {
	i.operationID = operationID
	i.artifact = proto.Clone(artifact).(*toolpb.Artifact)
	return 0, 0, nil
}

func (*recordingArtifactImporter) ArtifactTypes() []string { return nil }

type blockingArtifactImporter struct {
	started chan struct{}
	release chan struct{}
}

func (i *blockingArtifactImporter) ImportArtifact(context.Context, string, *toolpb.Artifact) (uint64, uint64, error) {
	close(i.started)
	<-i.release
	return 0, 0, nil
}

func (*blockingArtifactImporter) ArtifactTypes() []string { return nil }

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
