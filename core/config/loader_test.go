package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	src.ActiveProfile = "config-profile"
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
	if dst.ActiveProfile != "config-profile" {
		t.Errorf("ActiveProfile: got %q, want %q", dst.ActiveProfile, "config-profile")
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
  max_tokens: 32768
  context_window: 1000000
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
	if opt.MaxTokens != 32768 || opt.ContextWindow != 1000000 {
		t.Fatalf("model limits = max:%d context:%d", opt.MaxTokens, opt.ContextWindow)
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

	var option Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &option); err != nil {
		t.Fatal(err)
	}
	if option.SearchConfig.TavilyKeys != "K1,K2" {
		t.Fatalf("search config = %#v", option.SearchConfig)
	}
}

func TestLoadScanDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
scan:
  verify: critical
`)

	var option Option
	if err := LoadConfig(filepath.Join(dir, "aiscan.yaml"), &option); err != nil {
		t.Fatal(err)
	}
	if got := option.ScanConfig.Verify; got != "critical" {
		t.Errorf("VerifyMode: got %q, want %q", got, "critical")
	}
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
	for _, want := range []string{
		"output:",
		"preset: \"default\"",
		"# reasoning: \"hidden\"",
		"# tool_results: \"hidden\"",
		"# live_status: true",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated config missing %q", want)
		}
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
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
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
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
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
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
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
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
			t.Fatal(err)
		}
		if option.Provider != "anthropic" || option.BaseURL != "https://anthropic-proxy.example/v1" || option.Model != "claude-env" || option.APIKey != "anthropic-key" {
			t.Fatalf("Anthropic env aliases not applied: %#v", option.LLMOptions)
		}
	})
}

// A provider-scoped model env (ANTHROPIC_MODEL) is often injected by the
// surrounding environment for another tool (a Claude-Code style gateway). It must
// NOT override a model the user configured for aiscan itself — otherwise editing
// the model in the config file / Settings UI has no effect at runtime. AISCAN_MODEL
// (aiscan's own namespace) keeps overriding; the borrowed provider env only fills
// an empty slot.
func TestResolveRuntimeConfigConfigModelBeatsBorrowedProviderModelEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: anthropic
  base_url: https://kiro.example/v1
  api_key: config-key
  model: kimi-for-coding
`)
	t.Setenv("ANTHROPIC_MODEL", "claude-opus-4-8")

	withDefaults(t, func() {
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
			t.Fatal(err)
		}
		if option.Model != "kimi-for-coding" {
			t.Errorf("config model should win over borrowed ANTHROPIC_MODEL: got %q, want %q", option.Model, "kimi-for-coding")
		}
	})

	// With no model in the config, the borrowed provider env still fills the gap.
	withDefaults(t, func() {
		emptyDir := t.TempDir()
		writeTestConfig(t, emptyDir, "llm:\n  provider: anthropic\n  base_url: https://kiro.example/v1\n  api_key: config-key\n")
		origDir, _ := os.Getwd()
		os.Chdir(emptyDir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
			t.Fatal(err)
		}
		if option.Model != "claude-opus-4-8" {
			t.Errorf("borrowed ANTHROPIC_MODEL should fill an empty model: got %q, want %q", option.Model, "claude-opus-4-8")
		}
	})
}

// Same borrowed-env hazard as the model case, but for base_url and api_key: a
// hub-launched agent inherits the hub's env, which on a Claude-Code style gateway
// exports ANTHROPIC_BASE_URL / ANTHROPIC_API_KEY. Those must NOT override the
// base URL / key the user saved via the Settings UI (the config file) — otherwise
// "启动本地 Agent … 模型/密钥沿用当前配置" silently uses the borrowed env instead.
func TestResolveRuntimeConfigConfigBaseURLAndKeyBeatBorrowedProviderEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: anthropic
  base_url: https://kiro.example/v1
  api_key: config-key
  model: kimi-for-coding
`)
	t.Setenv("ANTHROPIC_BASE_URL", "https://borrowed.example/v1")
	t.Setenv("ANTHROPIC_API_KEY", "borrowed-key")

	withDefaults(t, func() {
		origDir, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
			t.Fatal(err)
		}
		if option.BaseURL != "https://kiro.example/v1" {
			t.Errorf("config base_url should win over borrowed ANTHROPIC_BASE_URL: got %q, want %q", option.BaseURL, "https://kiro.example/v1")
		}
		if option.APIKey != "config-key" {
			t.Errorf("config api_key should win over borrowed ANTHROPIC_API_KEY: got %q, want %q", option.APIKey, "config-key")
		}
	})

	// With no base_url / api_key in the config, the borrowed provider env still
	// fills the gap (unchanged fallback behavior).
	withDefaults(t, func() {
		emptyDir := t.TempDir()
		writeTestConfig(t, emptyDir, "llm:\n  provider: anthropic\n  model: kimi-for-coding\n")
		origDir, _ := os.Getwd()
		os.Chdir(emptyDir)
		defer os.Chdir(origDir)

		option := Option{}
		if _, err := ResolveRuntimeConfig(&option, true, testProviderInference); err != nil {
			t.Fatal(err)
		}
		if option.BaseURL != "https://borrowed.example/v1" {
			t.Errorf("borrowed ANTHROPIC_BASE_URL should fill an empty base_url: got %q, want %q", option.BaseURL, "https://borrowed.example/v1")
		}
		if option.APIKey != "borrowed-key" {
			t.Errorf("borrowed ANTHROPIC_API_KEY should fill an empty api_key: got %q, want %q", option.APIKey, "borrowed-key")
		}
	})
}

func TestResolveRuntimeConfigCandidateUsesStagedProfileAndExplicitCLIOverrides(t *testing.T) {
	for _, key := range []string{
		"AISCAN_PROVIDER", "AISCAN_LLM_PROVIDER", "AISCAN_MODEL", "AISCAN_LLM_MODEL",
		"AISCAN_BASE_URL", "AISCAN_BASEURL", "AISCAN_LLM_BASE_URL", "AISCAN_LLM_BASEURL",
		"AISCAN_API_KEY", "AISCAN_LLM_API_KEY", "OPENAI_MODEL", "OPENAI_BASE_URL", "OPENAI_API_KEY",
	} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  active_profile: staged
  providers:
    - id: old
      provider: anthropic
      api_key: old-key
      model: old-model
    - id: staged
      provider: openai
      base_url: https://staged.example/v1
      api_key: staged-key
      model: staged-model
`)
	path := filepath.Join(dir, "aiscan.yaml")

	staged := Option{MiscOptions: MiscOptions{ConfigFile: path}}
	if _, err := ResolveRuntimeConfig(&staged, false, testProviderInference); err != nil {
		t.Fatal(err)
	}
	if staged.ActiveProfile != "staged" || len(staged.Providers) != 2 || staged.Providers[1].Model != "staged-model" {
		t.Fatalf("staged profile was not loaded: option=%+v", staged.LLMOptions)
	}

	explicit := Option{
		MiscOptions: MiscOptions{ConfigFile: path},
		LLMOptions:  LLMOptions{Provider: "deepseek", Model: "cli-model", APIKey: "cli-key"},
	}
	if _, err := ResolveRuntimeConfig(&explicit, false, testProviderInference); err != nil {
		t.Fatal(err)
	}
	if explicit.Provider != "deepseek" || explicit.Model != "cli-model" || explicit.APIKey != "cli-key" {
		t.Fatalf("explicit CLI LLM values did not override staged config: %+v", explicit.LLMOptions)
	}
}

func testProviderInference(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "anthropic") {
		return "anthropic"
	}
	return "openai"
}

func withDefaults(t *testing.T, fn func()) {
	t.Helper()
	saved := []*string{
		&DefaultProvider, &DefaultBaseURL, &DefaultAPIKey, &DefaultModel,
		&DefaultScannerProxy, &DefaultCyberhubURL, &DefaultCyberhubKey,
		&DefaultCyberhubMode, &DefaultVerify,
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
