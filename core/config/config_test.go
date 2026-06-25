package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func writeTestConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "aiscan.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMergeOptionOnlyFillsEmpty(t *testing.T) {
	dst := Option{}
	dst.Provider = "cli-provider"
	dst.Model = ""

	src := Option{}
	src.Provider = "config-provider"
	src.Model = "config-model"
	src.CyberhubURL = "http://config-hub:9000"

	mergeOption(&dst, &src)

	if dst.Provider != "cli-provider" {
		t.Errorf("Provider: got %q, want %q (CLI should win)", dst.Provider, "cli-provider")
	}
	if dst.Model != "config-model" {
		t.Errorf("Model: got %q, want %q (config should fill empty)", dst.Model, "config-model")
	}
	if dst.CyberhubURL != "http://config-hub:9000" {
		t.Errorf("CyberhubURL: got %q, want %q", dst.CyberhubURL, "http://config-hub:9000")
	}
}

func TestMergeOptionSpaceDefault(t *testing.T) {
	dst := Option{}
	dst.Space = "default"

	src := Option{}
	src.Space = "production"

	mergeOption(&dst, &src)

	if dst.Space != "production" {
		t.Errorf("Space: got %q, want %q (config should override go-flags default)", dst.Space, "production")
	}
}

func TestMergeOptionSpaceExplicitCLI(t *testing.T) {
	dst := Option{}
	dst.Space = "cli-space"

	src := Option{}
	src.Space = "config-space"

	mergeOption(&dst, &src)

	if dst.Space != "cli-space" {
		t.Errorf("Space: got %q, want %q (CLI should win)", dst.Space, "cli-space")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: deepseek
  model: deepseek-chat
  base_url: https://api.deepseek.com/v1
cyberhub:
  url: http://hub:9000
  key: testkey
  mode: override
agent:
  web_url: http://web:8080
ioa:
  url: http://ioa:8765
  space: case-1
`)

	var opt Option
	err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &opt)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct{ field, got, want string }{
		{"Provider", opt.Provider, "deepseek"},
		{"Model", opt.Model, "deepseek-chat"},
		{"BaseURL", opt.BaseURL, "https://api.deepseek.com/v1"},
		{"CyberhubURL", opt.CyberhubURL, "http://hub:9000"},
		{"CyberhubKey", opt.CyberhubKey, "testkey"},
		{"CyberhubMode", opt.CyberhubMode, "override"},
		{"WebURL", opt.WebURL, "http://web:8080"},
		{"IOAURL", opt.IOAURL, "http://ioa:8765"},
		{"Space", opt.Space, "case-1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
}

func TestLoadConfigReconNumericZeroIsExplicit(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
recon:
  limit: 0
`)

	var opt Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &opt); err != nil {
		t.Fatal(err)
	}
	if opt.ReconLimit == nil || *opt.ReconLimit != 0 {
		t.Fatalf("ReconLimit = %#v, want explicit 0", opt.ReconLimit)
	}
}

func TestMergeOptionReconExplicitZeroWins(t *testing.T) {
	zeroInt := 0
	cfgLimit := 10
	dst := Option{ReconOptions: ReconOptions{
		ReconLimit: &zeroInt,
	}}
	src := Option{ReconOptions: ReconOptions{
		ReconLimit: &cfgLimit,
	}}

	mergeOption(&dst, &src)

	if *dst.ReconLimit != 0 {
		t.Fatalf("explicit zero was overwritten: %#v", dst.ReconOptions)
	}
}

func TestLoadConfigEmptyFieldsAreZero(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: ""
  model: ""
cyberhub:
  url: ""
`)

	var opt Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &opt); err != nil {
		t.Fatal(err)
	}
	if opt.Provider != "" {
		t.Errorf("Provider should be empty, got %q", opt.Provider)
	}
	if opt.Model != "" {
		t.Errorf("Model should be empty, got %q", opt.Model)
	}
	if opt.CyberhubURL != "" {
		t.Errorf("CyberhubURL should be empty, got %q", opt.CyberhubURL)
	}
}

func TestPriorityCLIOverConfig(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: config-provider
  model: config-model
  api_key: config-key
cyberhub:
  url: http://config-hub:9000
`)

	option := Option{}
	option.Provider = "cli-provider"
	option.APIKey = "cli-key"

	var loaded Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &loaded); err != nil {
		t.Fatal(err)
	}
	mergeOption(&option, &loaded)

	if option.Provider != "cli-provider" {
		t.Errorf("Provider: got %q, want %q (CLI wins)", option.Provider, "cli-provider")
	}
	if option.APIKey != "cli-key" {
		t.Errorf("APIKey: got %q, want %q (CLI wins)", option.APIKey, "cli-key")
	}
	if option.Model != "config-model" {
		t.Errorf("Model: got %q, want %q (config fills empty)", option.Model, "config-model")
	}
	if option.CyberhubURL != "http://config-hub:9000" {
		t.Errorf("CyberhubURL: got %q, want %q (config fills empty)", option.CyberhubURL, "http://config-hub:9000")
	}
}

func TestPriorityCustomConfigSameAsMerge(t *testing.T) {
	dir := t.TempDir()
	customPath := writeTestConfig(t, dir, `
llm:
  provider: custom-provider
  model: custom-model
`)

	option := Option{}
	option.Provider = "cli-provider"
	option.ConfigFile = customPath

	var loaded Option
	if err := LoadConfig(customPath, &loaded); err != nil {
		t.Fatal(err)
	}
	mergeOption(&option, &loaded)

	if option.Provider != "cli-provider" {
		t.Errorf("Provider: got %q, want %q (CLI > -c)", option.Provider, "cli-provider")
	}
	if option.Model != "custom-model" {
		t.Errorf("Model: got %q, want %q (-c fills empty)", option.Model, "custom-model")
	}
}

func TestPriorityConfigOverBuild(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: config-provider
`)

	withDefaults(t, func() {
		DefaultProvider = "build-provider"

		option := Option{}
		var loaded Option
		if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &loaded); err != nil {
			t.Fatal(err)
		}
		mergeOption(&option, &loaded)

		if option.Provider != "config-provider" {
			t.Errorf("Provider: got %q, want %q (config fills empty)", option.Provider, "config-provider")
		}

		ApplyDefaults(&option)
		if option.Provider != "config-provider" {
			t.Errorf("Provider after ApplyDefaults: got %q, want %q (config > build)", option.Provider, "config-provider")
		}
	})
}

func TestPriorityBuildFillsRemaining(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: ""
`)

	withDefaults(t, func() {
		DefaultModel = "build-model"

		option := Option{}
		var loaded Option
		if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &loaded); err != nil {
			t.Fatal(err)
		}
		mergeOption(&option, &loaded)

		if option.Model != "" {
			t.Errorf("Model before ApplyDefaults: got %q, want empty", option.Model)
		}

		ApplyDefaults(&option)
		if option.Model != "build-model" {
			t.Errorf("Model after ApplyDefaults: got %q, want %q (build fills remaining)", option.Model, "build-model")
		}
	})
}

func TestLoadConfigSearchOptions(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
search:
  tavily_keys: "K1,K2"
`)

	withDefaults(t, func() {
		if err := loadRuntimeDefaults(filepath.Join(dir, "aiscan.yaml")); err != nil {
			t.Fatal(err)
		}

		cfg := AppConfig(&Option{}, RuntimeFeatures{ToolsEnabled: true}, telemetry.NopLogger())
		if cfg.Tools.TavilyKeys != "K1,K2" {
			t.Fatalf("tool config = %#v", cfg.Tools)
		}
	})
}

func TestLoadScanDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
scan:
  verify: critical
  verify_timeout: 90
`)

	withDefaults(t, func() {
		if err := loadRuntimeDefaults(filepath.Join(dir, "aiscan.yaml")); err != nil {
			t.Fatal(err)
		}

		if DefaultVerify != "critical" {
			t.Errorf("DefaultVerify: got %q, want %q", DefaultVerify, "critical")
		}
		if DefaultVerifyTimeout != "90" {
			t.Errorf("DefaultVerifyTimeout: got %q, want %q", DefaultVerifyTimeout, "90")
		}
	})
}

func TestLoadAndApplyConfigDefaultFile(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: found-provider
`)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	option := Option{}
	path, err := LoadAndApplyConfig(&option)
	if err != nil {
		t.Fatal(err)
	}

	if path == "" {
		t.Fatal("expected aiscan.yaml to be found")
	}
	if option.Provider != "found-provider" {
		t.Errorf("Provider: got %q, want %q", option.Provider, "found-provider")
	}
}

func TestLoadAndApplyConfigCustomFile(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: default-provider
  model: default-model
`)
	customDir := t.TempDir()
	customPath := writeTestConfig(t, customDir, `
llm:
  provider: custom-provider
`)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	option := Option{}
	option.ConfigFile = customPath
	path, err := LoadAndApplyConfig(&option)
	if err != nil {
		t.Fatal(err)
	}

	if path != customPath {
		t.Errorf("path: got %q, want %q", path, customPath)
	}
	if option.Provider != "custom-provider" {
		t.Errorf("Provider: got %q, want %q (-c wins over default)", option.Provider, "custom-provider")
	}
	if option.Model != "" {
		t.Errorf("Model: got %q, want empty (-c replaces default config, not merges)", option.Model)
	}
}

func TestLoadAndApplyConfigRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aiscan.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  provider: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	option := Option{}
	option.ConfigFile = path
	gotPath, err := LoadAndApplyConfig(&option)
	if err == nil {
		t.Fatal("expected malformed config to return an error")
	}
	if gotPath != path {
		t.Errorf("path: got %q, want %q", gotPath, path)
	}
	if option.Provider != "" {
		t.Errorf("Provider: got %q, want empty after failed config load", option.Provider)
	}
}

func TestLoadAndApplyConfigRejectsMissingExplicitFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	option := Option{}
	option.ConfigFile = path

	gotPath, err := LoadAndApplyConfig(&option)
	if err == nil {
		t.Fatal("expected missing explicit config to return an error")
	}
	if gotPath != "" {
		t.Errorf("path: got %q, want empty", gotPath)
	}
}

func TestInitDefaultConfig(t *testing.T) {
	content := InitDefaultConfig()
	if len(content) < 100 {
		t.Error("generated config too short")
	}

	var opt Option
	dir := t.TempDir()
	path := filepath.Join(dir, "aiscan.yaml")
	os.WriteFile(path, []byte(content), 0o644)
	if err := LoadConfig(path, &opt); err != nil {
		t.Errorf("generated config should be parseable: %v", err)
	}
}

func TestFullPriorityChain(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: config-provider
  model: config-model
  api_key: config-key
cyberhub:
  url: http://config-hub:9000
  proxy: config-proxy
`)

	withDefaults(t, func() {
		DefaultProvider = "build-provider"
		DefaultModel = "build-model"
		DefaultScannerProxy = "build-proxy"

		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		option.Provider = "cli-provider"

		if _, err := LoadAndApplyConfig(&option); err != nil {
			t.Fatal(err)
		}
		ApplyDefaults(&option)

		checks := []struct{ field, got, want, reason string }{
			{"Provider", option.Provider, "cli-provider", "CLI > config > build"},
			{"Model", option.Model, "config-model", "config > build (CLI empty)"},
			{"APIKey", option.APIKey, "config-key", "config fills empty"},
			{"Proxy", option.Proxy, "config-proxy", "config > build"},
			{"CyberhubURL", option.CyberhubURL, "http://config-hub:9000", "config fills empty"},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s: got %q, want %q (%s)", c.field, c.got, c.want, c.reason)
			}
		}
	})
}

func TestResolveRuntimeConfigEnvOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: deepseek
  base_url: https://config.example/v1
  api_key: config-key
  model: config-model
  proxy: http://config-proxy:7890
cyberhub:
  url: http://config-hub:9000
`)
	t.Setenv("AISCAN_MODEL", "env-model")
	t.Setenv("AISCAN_BASE_URL", "https://env.example/v1")
	t.Setenv("AISCAN_API_KEY", "env-key")
	t.Setenv("AISCAN_LLM_PROXY", "http://env-proxy:7890")
	t.Setenv("CYBERHUB_URL", "http://env-hub:9000")

	withDefaults(t, func() {
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option); err != nil {
			t.Fatal(err)
		}

		checks := []struct{ field, got, want string }{
			{"Provider", option.Provider, "deepseek"},
			{"BaseURL", option.BaseURL, "https://env.example/v1"},
			{"APIKey", option.APIKey, "env-key"},
			{"Model", option.Model, "env-model"},
			{"LLMProxy", option.LLMProxy, "http://env-proxy:7890"},
			{"CyberhubURL", option.CyberhubURL, "http://env-hub:9000"},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
			}
		}
	})
}

func TestResolveRuntimeConfigCLIWinsOverEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  model: config-model
`)
	t.Setenv("AISCAN_MODEL", "env-model")
	t.Setenv("AISCAN_BASE_URL", "https://env.example/v1")
	t.Setenv("AISCAN_API_KEY", "env-key")

	withDefaults(t, func() {
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		option.Model = "cli-model"
		option.BaseURL = "https://cli.example/v1"
		option.APIKey = "cli-key"
		if _, err := ResolveRuntimeConfig(&option); err != nil {
			t.Fatal(err)
		}
		if option.Model != "cli-model" || option.BaseURL != "https://cli.example/v1" || option.APIKey != "cli-key" {
			t.Fatalf("CLI values were overridden: %#v", option.LLMOptions)
		}
	})
}

func TestResolveRuntimeConfigSupportsOpenAIEnvAliases(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://openai-proxy.example/v1")
	t.Setenv("OPENAI_MODEL", "gpt-env")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	withDefaults(t, func() {
		dir := t.TempDir()
		writeTestConfig(t, dir, `
llm:
  provider: ""
`)
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option); err != nil {
			t.Fatal(err)
		}
		if option.Provider != "openai" || option.BaseURL != "https://openai-proxy.example/v1" || option.Model != "gpt-env" || option.APIKey != "openai-key" {
			t.Fatalf("OpenAI env aliases not applied: %#v", option.LLMOptions)
		}
	})
}

func TestResolveRuntimeConfigSupportsAnthropicEnvAliases(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic-proxy.example/v1")
	t.Setenv("ANTHROPIC_MODEL", "claude-env")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")

	withDefaults(t, func() {
		dir := t.TempDir()
		writeTestConfig(t, dir, `
llm:
  provider: ""
`)
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option); err != nil {
			t.Fatal(err)
		}
		if option.Provider != "anthropic" || option.BaseURL != "https://anthropic-proxy.example/v1" || option.Model != "claude-env" || option.APIKey != "anthropic-key" {
			t.Fatalf("Anthropic env aliases not applied: %#v", option.LLMOptions)
		}
	})
}

func TestProvidersListOnly(t *testing.T) {
	option := Option{}
	option.Providers = []LLMProviderEntry{
		{Provider: "deepseek", APIKey: "key1", Model: "deepseek-chat"},
		{Provider: "openai", APIKey: "key2", Model: "gpt-4o"},
	}

	primary := ProviderConfig(&option)
	if primary.Provider != "deepseek" || primary.APIKey != "key1" || primary.Model != "deepseek-chat" {
		t.Errorf("primary should be providers[0], got %+v", primary)
	}

	fallbacks := FallbackProviderConfigs(&option)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "openai" || fallbacks[0].Model != "gpt-4o" {
		t.Errorf("fallback should be providers[1:], got %+v", fallbacks)
	}
}

func TestProvidersListWithSingleFields(t *testing.T) {
	option := Option{}
	option.Provider = "anthropic"
	option.APIKey = "cli-key"
	option.Providers = []LLMProviderEntry{
		{Provider: "deepseek", APIKey: "fb1", Model: "deepseek-chat"},
	}

	primary := ProviderConfig(&option)
	if primary.Provider != "anthropic" || primary.APIKey != "cli-key" {
		t.Errorf("single fields should win when set, got %+v", primary)
	}

	fallbacks := FallbackProviderConfigs(&option)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "deepseek" {
		t.Errorf("providers should be fallback when single fields set, got %+v", fallbacks)
	}
}

func TestProvidersListFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  providers:
    - provider: deepseek
      api_key: dk-111
      model: deepseek-chat
    - provider: openai
      api_key: sk-222
      model: gpt-4o
`)

	var opt Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &opt); err != nil {
		t.Fatal(err)
	}
	if len(opt.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(opt.Providers))
	}

	primary := ProviderConfig(&opt)
	if primary.Provider != "deepseek" || primary.APIKey != "dk-111" {
		t.Errorf("primary from list: %+v", primary)
	}

	fallbacks := FallbackProviderConfigs(&opt)
	if len(fallbacks) != 1 || fallbacks[0].APIKey != "sk-222" {
		t.Errorf("fallbacks from list: %+v", fallbacks)
	}
}

func withDefaults(t *testing.T, fn func()) {
	t.Helper()
	saved := []*string{
		&DefaultProvider, &DefaultBaseURL, &DefaultAPIKey, &DefaultModel,
		&DefaultScannerProxy, &DefaultCyberhubURL, &DefaultCyberhubKey,
		&DefaultCyberhubMode, &DefaultVerify, &DefaultVerifyTimeout,
		&DefaultTavilyKeys, &DefaultIOAURL, &DefaultIOANodeID,
		&DefaultIOANodeName, &DefaultSpace,
	}
	originals := make([]string, len(saved))
	for i, p := range saved {
		originals[i] = *p
	}
	t.Cleanup(func() {
		for i, p := range saved {
			*p = originals[i]
		}
	})
	fn()
}
