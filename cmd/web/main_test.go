package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/web"
)

func TestLLMConfigFileStoreReadsAndWritesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`llm:
  provider: ""
  base_url: ""
  api_key: ""
  model: ""
  proxy: ""
cyberhub:
  mode: "merge"
`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store := &llmConfigFileStore{explicit: path}
	cfg, err := store.GetLLMConfig(context.Background())
	if err != nil {
		t.Fatalf("GetLLMConfig() error = %v", err)
	}
	if !cfg.ConfigLoaded || cfg.Provider != "" || cfg.APIKeyConfigured {
		t.Fatalf("initial config = %#v", cfg)
	}

	saved, err := store.SaveLLMConfig(context.Background(), web.LLMConfig{
		Provider: "ollama",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Model:    "qwen2.5",
	})
	if err != nil {
		t.Fatalf("SaveLLMConfig() error = %v", err)
	}
	if saved.Provider != "ollama" || saved.Model != "qwen2.5" || saved.APIKeyConfigured {
		t.Fatalf("saved config = %#v", saved)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		`provider: ollama`,
		`base_url: http://127.0.0.1:11434/v1`,
		`model: qwen2.5`,
		`cyberhub:`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
}
