package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/proto"
)

func TestHubConfigPreservesRuntimeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	original := "llm:\n  active_profile: main\n  providers:\n    - id: main\n      provider: openai\n      model: old\n      api_key: stored-secret\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	codec := SharedConfigCodec()
	env := &cfg.Context{LookupEnv: func(name string) (string, bool) {
		if name == "CYBER_API_KEY" {
			return "environment-secret", true
		}
		return "", false
	}}
	explicit := cfg.Option{Sections: codec.Sections, Context: env, MiscOptions: cfg.MiscOptions{ConfigFile: path}}
	option := explicit
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		t.Fatal(err)
	}
	store := &FileConfigStore{Explicit: path, Runtime: &option, Overrides: &explicit, Codec: codec}
	_, loaded, current, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !loaded || len(current.Extensions) != 0 || current.Agent != nil {
		t.Fatalf("Hub materialized product or agent settings: %v", current)
	}
	next := proto.CloneOf(current)
	next.Llm.Providers[0].Model = "new"
	next.Llm.Providers[0].ApiKey = ""
	prepared, err := store.PrepareDistributeConfig(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	before, err := os.ReadFile(path)
	if err != nil || string(before) != original {
		t.Fatalf("staging changed target: %v", err)
	}
	if prepared.Config.Llm.Providers[0].ApiKey != "stored-secret" {
		t.Fatal("stored key was lost")
	}
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stored-secret", "model: new"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("saved config missing %q: %s", want, after)
		}
	}
	if strings.Contains(string(after), "environment-secret") {
		t.Fatal("environment key was persisted")
	}
	if got := store.RuntimeLLM(); got.APIKey != "environment-secret" || got.Model != "new" {
		t.Fatalf("effective provider after commit: model=%q key preserved=%v", got.Model, got.APIKey == "environment-secret")
	}
}

func TestHubConfigCanCreateFirstFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.yaml")
	store := &FileConfigStore{Explicit: path, Codec: SharedConfigCodec()}
	prepared, err := store.PrepareDistributeConfig(t.Context(), &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "main", Model: "test", ApiKey: "key"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	if got := store.RuntimeLLM(); got.Model != "test" {
		t.Fatalf("runtime model = %q", got.Model)
	}
}
