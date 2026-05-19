package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMergeOptionOnlyFillsEmpty verifies mergeOption does not overwrite
// existing (non-empty) values in dst.
func TestMergeOptionOnlyFillsEmpty(t *testing.T) {
	dst := Option{}
	dst.Provider = "cli-provider"
	dst.Model = "" // empty, should be filled

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

// TestMergeOptionSpaceDefault verifies that go-flags default:"default" can
// still be overridden by config when the value is exactly "default".
func TestMergeOptionSpaceDefault(t *testing.T) {
	dst := Option{}
	dst.Space = "default" // go-flags default

	src := Option{}
	src.Space = "production"

	mergeOption(&dst, &src)

	if dst.Space != "production" {
		t.Errorf("Space: got %q, want %q (config should override go-flags default)", dst.Space, "production")
	}
}

// TestMergeOptionSpaceExplicitCLI verifies that an explicit CLI value
// for --space is NOT overridden by config.
func TestMergeOptionSpaceExplicitCLI(t *testing.T) {
	dst := Option{}
	dst.Space = "cli-space" // explicit CLI value

	src := Option{}
	src.Space = "config-space"

	mergeOption(&dst, &src)

	if dst.Space != "cli-space" {
		t.Errorf("Space: got %q, want %q (CLI should win)", dst.Space, "cli-space")
	}
}

// TestLoadConfig verifies gookit/config YAML loading with config: tags.
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: deepseek
  model: deepseek-chat
  base_url: https://api.deepseek.com/v1
vision:
  enabled: true
  provider: openrouter
  base_url: https://openrouter.ai/api/v1
  api_key: vision-key
  model: qwen/qwen3.6-flash
  proxy: http://127.0.0.1:7890
cyberhub:
  url: http://hub:9000
  key: testkey
  mode: override
ioa:
  url: http://ioa:8765
  space: case-1
`)

	var opt Option
	err := LoadConfig(filepath.Join(dir, "config.yaml"), &opt)
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
		{"IOAURL", opt.IOAURL, "http://ioa:8765"},
		{"Space", opt.Space, "case-1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
	if !opt.Vision {
		t.Error("Vision: got false, want true (from vision.enabled)")
	}
	visionChecks := []struct{ field, got, want string }{
		{"VisionProvider", opt.VisionProvider, "openrouter"},
		{"VisionBaseURL", opt.VisionBaseURL, "https://openrouter.ai/api/v1"},
		{"VisionAPIKey", opt.VisionAPIKey, "vision-key"},
		{"VisionModel", opt.VisionModel, "qwen/qwen3.6-flash"},
		{"VisionProxy", opt.VisionProxy, "http://127.0.0.1:7890"},
	}
	for _, c := range visionChecks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
}

// TestLoadConfigEmptyFieldsAreZero verifies empty YAML values don't produce
// non-empty Go strings.
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
	if err := LoadConfig(filepath.Join(dir, "config.yaml"), &opt); err != nil {
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

func TestAppConfigUsesIndependentVisionProvider(t *testing.T) {
	option := Option{
		LLMOptions: LLMOptions{
			Provider: "deepseek",
			BaseURL:  "https://api.deepseek.com/v1",
			APIKey:   "main-key",
			Model:    "deepseek-v4-pro",
		},
		VisionOptions: VisionOptions{
			VisionBaseURL: "https://openrouter.ai/api/v1",
			VisionAPIKey:  "vision-key",
			VisionModel:   "qwen/qwen3.6-flash",
		},
	}

	cfg := appConfig(&option, runtimeFeatures{ProviderEnabled: true, ToolsEnabled: true}, nil)
	if !cfg.Tools.VisionEnabled {
		t.Fatal("vision tool should be enabled when independent vision config is set")
	}
	if !cfg.Vision.Enabled {
		t.Fatal("independent vision provider config should be enabled")
	}
	if cfg.Vision.Config.Provider != "openrouter" {
		t.Fatalf("vision provider = %q, want openrouter", cfg.Vision.Config.Provider)
	}
	if cfg.Vision.Config.BaseURL != "https://openrouter.ai/api/v1" ||
		cfg.Vision.Config.APIKey != "vision-key" ||
		cfg.Vision.Config.Model != "qwen/qwen3.6-flash" {
		t.Fatalf("vision config = %#v", cfg.Vision.Config)
	}
}

func TestAppConfigVisionCanFallbackToMainProvider(t *testing.T) {
	option := Option{VisionOptions: VisionOptions{Vision: true}}

	cfg := appConfig(&option, runtimeFeatures{ProviderEnabled: true, ToolsEnabled: true}, nil)
	if !cfg.Tools.VisionEnabled {
		t.Fatal("vision tool should be enabled")
	}
	if cfg.Vision.Enabled {
		t.Fatal("independent vision provider should stay disabled without vision-specific config")
	}
}

// TestPriorityCLIOverConfig verifies CLI > config.yaml.
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

	// Simulate: CLI sets provider and api_key, config sets model and cyberhub
	option := Option{}
	option.Provider = "cli-provider"
	option.APIKey = "cli-key"

	var loaded Option
	if err := LoadConfig(filepath.Join(dir, "config.yaml"), &loaded); err != nil {
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

// TestPriorityCustomConfigSameAsMerge verifies -c also uses mergeOption
// (CLI still wins over -c).
func TestPriorityCustomConfigSameAsMerge(t *testing.T) {
	dir := t.TempDir()
	customPath := writeTestConfig(t, dir, `
llm:
  provider: custom-provider
  model: custom-model
`)

	option := Option{}
	option.Provider = "cli-provider" // CLI set
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

// TestPriorityConfigOverBuild verifies config > build ldflags.
func TestPriorityConfigOverBuild(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: config-provider
`)

	// Simulate build-time ldflags by setting Default vars
	origProvider := DefaultProvider
	DefaultProvider = "build-provider"
	defer func() { DefaultProvider = origProvider }()

	option := Option{} // CLI didn't set anything

	var loaded Option
	if err := LoadConfig(filepath.Join(dir, "config.yaml"), &loaded); err != nil {
		t.Fatal(err)
	}
	mergeOption(&option, &loaded)

	// Config fills empty
	if option.Provider != "config-provider" {
		t.Errorf("Provider: got %q, want %q (config fills empty)", option.Provider, "config-provider")
	}

	// Now applyDefaults should NOT override
	applyDefaults(&option)
	if option.Provider != "config-provider" {
		t.Errorf("Provider after applyDefaults: got %q, want %q (config > build)", option.Provider, "config-provider")
	}
}

// TestPriorityBuildFillsRemaining verifies build ldflags fill when config
// doesn't set a value.
func TestPriorityBuildFillsRemaining(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
llm:
  provider: ""
`)

	origModel := DefaultModel
	DefaultModel = "build-model"
	defer func() { DefaultModel = origModel }()

	option := Option{} // CLI didn't set, config is empty

	var loaded Option
	if err := LoadConfig(filepath.Join(dir, "config.yaml"), &loaded); err != nil {
		t.Fatal(err)
	}
	mergeOption(&option, &loaded)

	if option.Model != "" {
		t.Errorf("Model before applyDefaults: got %q, want empty", option.Model)
	}

	applyDefaults(&option)
	if option.Model != "build-model" {
		t.Errorf("Model after applyDefaults: got %q, want %q (build fills remaining)", option.Model, "build-model")
	}
}

// TestLoadScanDefaults verifies scan.verify and scan.verify_timeout are
// applied to Default* vars.
func TestLoadScanDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `
scan:
  verify: critical
  verify_timeout: 90
`)

	origVerify := DefaultVerify
	origTimeout := DefaultVerifyTimeout
	defer func() {
		DefaultVerify = origVerify
		DefaultVerifyTimeout = origTimeout
	}()

	loadScanDefaults(filepath.Join(dir, "config.yaml"))

	if DefaultVerify != "critical" {
		t.Errorf("DefaultVerify: got %q, want %q", DefaultVerify, "critical")
	}
	if DefaultVerifyTimeout != "90" {
		t.Errorf("DefaultVerifyTimeout: got %q, want %q", DefaultVerifyTimeout, "90")
	}
}

// TestLoadAndApplyConfigDefaultFile verifies auto-discovery of config.yaml
// in CWD.
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
	path := loadAndApplyConfig(&option)

	if path == "" {
		t.Fatal("expected config.yaml to be found")
	}
	if option.Provider != "found-provider" {
		t.Errorf("Provider: got %q, want %q", option.Provider, "found-provider")
	}
}

// TestLoadAndApplyConfigCustomFile verifies -c takes precedence over
// default config.yaml in CWD.
func TestLoadAndApplyConfigCustomFile(t *testing.T) {
	dir := t.TempDir()
	// Default config in CWD
	writeTestConfig(t, dir, `
llm:
  provider: default-provider
  model: default-model
`)
	// Custom config
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
	path := loadAndApplyConfig(&option)

	if path != customPath {
		t.Errorf("path: got %q, want %q", path, customPath)
	}
	if option.Provider != "custom-provider" {
		t.Errorf("Provider: got %q, want %q (-c wins over default)", option.Provider, "custom-provider")
	}
	// model not in custom config → stays empty (default config NOT loaded)
	if option.Model != "" {
		t.Errorf("Model: got %q, want empty (-c replaces default config, not merges)", option.Model)
	}
}

// TestInitDefaultConfig verifies --init generates valid YAML.
func TestInitDefaultConfig(t *testing.T) {
	content := InitDefaultConfig()
	if len(content) < 100 {
		t.Error("generated config too short")
	}

	// Verify it can be parsed
	var opt Option
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(content), 0o644)
	if err := LoadConfig(path, &opt); err != nil {
		t.Errorf("generated config should be parseable: %v", err)
	}
}

// TestFullPriorityChain is an integration test that verifies:
// CLI > config > build > hardcoded
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

	origProvider := DefaultProvider
	origModel := DefaultModel
	origScannerProxy := DefaultScannerProxy
	DefaultProvider = "build-provider"
	DefaultModel = "build-model"
	DefaultScannerProxy = "build-proxy"
	defer func() {
		DefaultProvider = origProvider
		DefaultModel = origModel
		DefaultScannerProxy = origScannerProxy
	}()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Simulate CLI: only --provider set
	option := Option{}
	option.Provider = "cli-provider"

	// Step 1: config loading
	loadAndApplyConfig(&option)
	// Step 2: apply build defaults
	applyDefaults(&option)

	checks := []struct{ field, got, want, reason string }{
		{"Provider", option.Provider, "cli-provider", "CLI > config > build"},
		{"Model", option.Model, "config-model", "config > build (CLI empty)"},
		{"APIKey", option.APIKey, "config-key", "config fills empty"},
		{"Proxy", option.ScannerOptions.Proxy, "config-proxy", "config > build"},
		{"CyberhubURL", option.CyberhubURL, "http://config-hub:9000", "config fills empty"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q (%s)", c.field, c.got, c.want, c.reason)
		}
	}
}
