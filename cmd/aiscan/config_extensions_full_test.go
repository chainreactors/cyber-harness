//go:build full

package main

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/pkg/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run configured by startup flags has no cyber.yaml on disk, but that is the
// configuration the process is actually using. The settings page must show it,
// and the first save must materialize those values instead of writing the blank
// secrets the form sends back.
func TestWebConfigStoreProjectsRuntimeFlagsWithoutConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	option := &cfg.Option{
		LLMOptions:     cfg.LLMOptions{Provider: "openai", BaseURL: "http://127.0.0.1:9/v1", APIKey: "flag-key", Model: "flag-model"},
		ScannerOptions: cfg.ScannerOptions{CyberhubURL: "http://cyberhub.test", CyberhubKey: "hub-key"},
		ReconOptions:   cfg.ReconOptions{FofaKey: "fofa-key"},
	}
	store := &webConfigStore{explicit: path, runtime: option}

	_, loaded, current, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded {
		t.Fatal("store reported a loaded config for a flags-only run")
	}
	active := cfg.ActiveLLMProvider(current.GetLlm())
	if active == nil || active.GetModel() != "flag-model" || active.GetApiKey() != "flag-key" {
		t.Fatalf("projected active provider = %+v", active)
	}

	// Exactly what the settings page posts back after loading the projection:
	// non-secret fields as shown, secrets blank.
	incoming := &types.DistributeConfig{
		Llm: &types.LLMConfig{
			ActiveProfile: active.GetId(),
			Providers: []*types.LLMProviderConfig{{
				Id: active.GetId(), Provider: active.GetProvider(),
				BaseUrl: active.GetBaseUrl(), Model: active.GetModel(),
			}},
		},
		Cyberhub: &types.CyberhubConfig{Url: "http://cyberhub.test"},
	}
	prepared, err := store.PrepareDistributeConfig(t.Context(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	if got := cfg.ActiveLLMProvider(prepared.Config.GetLlm()).GetApiKey(); got != "flag-key" {
		t.Fatalf("first save dropped the running API key: %q", got)
	}
	if got := prepared.Config.GetCyberhub().GetKey(); got != "hub-key" {
		t.Fatalf("first save dropped the running cyberhub key: %q", got)
	}
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	_, loaded, committed, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !loaded || cfg.ActiveLLMProvider(committed.GetLlm()).GetModel() != "flag-model" {
		t.Fatalf("committed flags config = %+v", committed)
	}
}

// An unset extension value survives structpb as a JSON null, which would reach
// the operator's file as an explicit `token: null`. A hand-written config omits
// the key instead, and every reader treats the two the same.
func TestMarshalProductConfigOmitsNullValues(t *testing.T) {
	extensions, err := cfg.ValuesToProto(cfg.Values{"ioa.client": {"url": "http://ioa.test", "token": nil}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalProductConfig(&types.DistributeConfig{Extensions: extensions}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Fatalf("marshaled config carries a null value:\n%s", data)
	}
	if !strings.Contains(string(data), "http://ioa.test") {
		t.Fatalf("marshaled config lost the extension:\n%s", data)
	}
}

func TestWebConfigExtensionSavePreservesOmittedSectionsAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	original := []byte("ioa:\n  url: http://ioa.test\n  token: secret\n  space: team\nextensions:\n  ioa.server:\n    token: server-secret\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store := &webConfigStore{explicit: path}
	for _, incoming := range []*types.DistributeConfig{{}, {Ioa: &types.IOAConfig{Url: "http://ioa.test", Space: ""}}} {
		prepared, err := store.PrepareDistributeConfig(t.Context(), incoming)
		if err != nil {
			t.Fatal(err)
		}
		defer store.DiscardDistributeConfig(prepared)
		values := cfg.ValuesFromProto(prepared.Config.Extensions)
		if values["ioa.client"]["token"] != "secret" || values["ioa.server"]["token"] != "server-secret" {
			t.Fatalf("secrets lost: %#v", values)
		}
		if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
			t.Fatal(err)
		}
	}
	_, _, stored, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ValuesFromProto(stored.Extensions)["ioa.client"]["space"] != "" {
		t.Fatal("explicit empty space not saved")
	}
	before, _ := os.ReadFile(path)
	invalid, _ := cfg.ValuesToProto(cfg.Values{"ioa.client": {"url": "invalid"}})
	if prepared, err := store.PrepareDistributeConfig(t.Context(), &types.DistributeConfig{Extensions: invalid}); err == nil || prepared != nil {
		t.Fatal("invalid extension staged")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("validation failure changed file")
	}
}
