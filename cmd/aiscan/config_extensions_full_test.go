//go:build full

package main

import (
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWebLayeredExtensionSavePreservesOwnership(t *testing.T) {
	root := t.TempDir()
	environment := &cfg.Context{
		Directory: filepath.Join(root, "project"), Home: filepath.Join(root, "user"),
		Executable: filepath.Join(root, "bin", "app"),
		LookupEnv:  func(string) (string, bool) { return "", false },
	}
	userPath := environment.UserFile()
	projectPath := filepath.Join(environment.Directory, cfg.DefaultConfigName)
	userData := []byte("extensions:\n  ioa.client:\n    url: http://ioa.test\n    token: inherited-secret\n    space: user-space\n")
	projectData := []byte("extensions:\n  ioa.client:\n    space: original-project\n  other-host:\n    credential: keep-private\n    rules: [one, two]\n")
	for path, data := range map[string][]byte{userPath: userData, projectPath: projectData} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := &webConfigStore{runtime: &cfg.Option{Context: environment}}
	var saved []byte
	for i := 0; i < 2; i++ {
		_, _, current, err := store.GetDistributeConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		values := cfg.ValuesFromProto(current.Extensions)
		if values["ioa.client"]["token"] != "inherited-secret" {
			t.Fatal("file layers did not preserve extension precedence and secrets")
		}
		if _, exists := values["other-host"]; exists {
			t.Fatal("unavailable extension entered runtime projection")
		}
		delete(values["ioa.client"], "token") // The editor submits the redacted view.
		values["ioa.client"]["space"] = "project-space"
		current.Extensions, err = cfg.ValuesToProto(values)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := store.PrepareDistributeConfig(t.Context(), current)
		if err != nil {
			t.Fatal(err)
		}
		defer store.DiscardDistributeConfig(prepared)
		if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(projectPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "inherited-secret") || strings.Contains(string(data), "http://ioa.test") {
			t.Fatal("extension save materialized inherited settings")
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		extensions := document["extensions"].(map[string]any)
		if extensions["other-host"].(map[string]any)["credential"] != "keep-private" {
			t.Fatal("extension save lost unavailable settings")
		}
		if i > 0 && string(data) != string(saved) {
			t.Fatal("repeated extension save changed the file")
		}
		saved = data
	}
	unchanged, err := os.ReadFile(userPath)
	if err != nil || string(unchanged) != string(userData) {
		t.Fatal("project save changed user configuration", err)
	}
	option := &cfg.Option{Context: environment, Sections: defaultSections(), Explicit: map[string]bool{}}
	if _, err := cfg.ResolveRuntimeConfig(option); err != nil {
		t.Fatal(err)
	}
	if option.Extensions["ioa.client"]["space"] != "project-space" || option.Extensions["ioa.client"]["token"] != "inherited-secret" {
		t.Fatal("restart lost edited extension configuration")
	}
}

// Runtime credentials must never be materialized by a settings save.
func TestWebConfigStoreDoesNotPersistRuntimeCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	option := &cfg.Option{LLMOptions: cfg.LLMOptions{Provider: "openai", APIKey: "flag-secret", Model: "flag-model"}, Extensions: cfg.Values{scannerext.CyberhubConfigKey: {"key": "hub-secret"}}}
	store := &webConfigStore{explicit: path, runtime: option}
	_, loaded, current, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded || len(current.GetLlm().GetProviders()) != 0 {
		t.Fatal("editable view mixed in runtime overrides")
	}
	incoming := &types.DistributeConfig{Llm: &types.LLMConfig{Providers: []*types.LLMProviderConfig{{Id: "saved", Provider: "openai", Model: "saved-model"}}}}
	prepared, err := store.PrepareDistributeConfig(t.Context(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "flag-secret") || strings.Contains(string(data), "hub-secret") {
		t.Fatal("runtime secrets leaked into settings")
	}
	_, loaded, current, err = store.GetDistributeConfig(t.Context())
	if err != nil || !loaded || cfg.ActiveLLMProvider(current.Llm).Model != "saved-model" {
		t.Fatal("save was not persisted", err)
	}
}

func TestNewProfileDoesNotInheritSecretByPosition(t *testing.T) {
	old := &types.LLMConfig{Providers: []*types.LLMProviderConfig{{Id: "original", ApiKey: "original-secret"}}}
	next := &types.LLMConfig{Providers: []*types.LLMProviderConfig{{Id: "new-profile", Provider: "openai", Model: "new-model"}}}
	preserveLLMProfileSecrets(next, old)
	if next.Providers[0].ApiKey != "" {
		t.Fatal("new profile inherited another profile's credentials")
	}
}

func TestWebLayeredSaveDoesNotCopyInheritedCredentials(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	user := filepath.Join(root, "user")
	_ = os.MkdirAll(project, 0700)
	_ = os.MkdirAll(filepath.Join(user, ".cyber"), 0700)
	userFile := filepath.Join(user, ".cyber", "cyber.yaml")
	projectFile := filepath.Join(project, "cyber.yaml")
	original := []byte("llm:\n  active_profile: work\n  providers:\n    - id: work\n      provider: openai\n      model: original\n      api_key: inherited-secret\n      base_url: https://example.test/v1\n")
	_ = os.WriteFile(userFile, original, 0600)
	_ = os.WriteFile(projectFile, []byte("agent:\n  timeout: 99\n"), 0600)
	environment := &cfg.Context{Directory: project, Home: user, Executable: filepath.Join(root, "bin", "app"), LookupEnv: func(string) (string, bool) { return "", false }}
	option := &cfg.Option{Context: environment, Explicit: map[string]bool{}, Sections: defaultSections()}
	if _, err := cfg.ResolveRuntimeConfig(option); err != nil {
		t.Fatal(err)
	}
	store := &webConfigStore{runtime: option}
	path, _, current, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if path != projectFile {
		t.Fatalf("target: %s", path)
	}
	current.Llm.Providers[0].Model = "edited"
	current.Llm.Providers[0].ApiKey = ""
	prepared, err := store.PrepareDistributeConfig(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	if prepared.Config.Llm.Providers[0].ApiKey != "inherited-secret" {
		t.Fatal("candidate lost inherited key")
	}
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(projectFile)
	if strings.Contains(string(data), "inherited-secret") || strings.Contains(string(data), "example.test") {
		t.Fatal("inherited values were materialized")
	}
	userAfter, _ := os.ReadFile(userFile)
	if string(original) != string(userAfter) {
		t.Fatal("user file was changed")
	}
	next := &cfg.Option{Context: environment, Explicit: map[string]bool{}, Sections: defaultSections()}
	if _, err := cfg.ResolveRuntimeConfig(next); err != nil {
		t.Fatal(err)
	}
	if next.Model != "edited" || next.APIKey != "inherited-secret" || next.Timeout != 99 {
		t.Fatal("restart did not reproduce effective configuration")
	}
}

// An unset extension value survives structpb as a JSON null, which would reach
// the operator's file as an explicit `token: null`. A hand-written config omits
// the key instead, and every reader treats the two the same.
func TestMarshalConfigOmitsNullValues(t *testing.T) {
	extensions, err := cfg.ValuesToProto(cfg.Values{"ioa.client": {"url": "http://ioa.test", "token": nil}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalConfig(&types.DistributeConfig{Extensions: extensions}, nil)
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
	original := []byte("extensions:\n  ioa.client:\n    url: http://ioa.test\n    token: secret\n    space: team\n  ioa.server:\n    token: server-secret\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store := &webConfigStore{explicit: path}
	clientExtension, err := cfg.ValuesToProto(cfg.Values{"ioa.client": {"url": "http://ioa.test", "space": ""}})
	if err != nil {
		t.Fatal(err)
	}
	for _, incoming := range []*types.DistributeConfig{{}, {Extensions: clientExtension}} {
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

func TestWebConfigScannerExtensionsPreserveSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	original := []byte("extensions:\n  cyberhub:\n    url: https://hub.example\n    key: hub-secret\n  recon:\n    fofa_key: fofa-secret\n    hunter_api_key: hunter-secret\n  scan:\n    verify: off\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store := &webConfigStore{explicit: path}
	fields, err := cfg.ValuesToProto(cfg.Values{
		"cyberhub": {"url": "https://hub.example", "key": ""},
		"recon":    {"fofa_key": "", "hunter_api_key": ""},
		"scan":     {"verify": "on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareDistributeConfig(t.Context(), &types.DistributeConfig{Extensions: fields})
	if err != nil {
		t.Fatal(err)
	}
	defer store.DiscardDistributeConfig(prepared)
	values := cfg.ValuesFromProto(prepared.Config.Extensions)
	if values["cyberhub"]["key"] != "hub-secret" || values["recon"]["fofa_key"] != "fofa-secret" || values["recon"]["hunter_api_key"] != "hunter-secret" || values["scan"]["verify"] != "on" {
		t.Fatalf("scanner extensions after edit: %#v", values)
	}
}
