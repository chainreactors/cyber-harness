package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedContext(t *testing.T) *Context {
	t.Helper()
	root := t.TempDir()
	c := &Context{Directory: filepath.Join(root, "project"), Home: filepath.Join(root, "user"), Executable: filepath.Join(root, "bin", "agent.exe"), LookupEnv: func(string) (string, bool) { return "", false }}
	for _, p := range []string{c.Directory, c.Home, filepath.Dir(c.Executable)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func putConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func resolvedFixture(t *testing.T, c *Context, explicit string) *Option {
	t.Helper()
	o := &Option{Context: c, Explicit: map[string]bool{}}
	o.ConfigFile = explicit
	if _, err := ResolveRuntimeConfig(o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestLayeredConfigurationAndExplicitIsolation(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  provider: openai\n  api_key: user-secret\n  model: user-model\nagent:\n  timeout: 99\n")
	project := filepath.Join(c.Directory, DefaultConfigName)
	putConfig(t, project, "llm:\n  model: project-model\nagent:\n  timeout: 0\nmisc:\n  data_dir: local-data\n")
	o := resolvedFixture(t, c, "")
	if o.Model != "project-model" || o.APIKey != "user-secret" || o.Timeout != 0 {
		t.Fatalf("layering failed")
	}
	if o.DataDir != filepath.Join(c.Directory, "local-data") {
		t.Fatalf("relative data path: %s", o.DataDir)
	}
	if len(o.Snapshot.Layers) != 2 || o.Snapshot.Sources["llm.model"] != project {
		t.Fatalf("sources: %+v", o.Snapshot.Sources)
	}
	isolate := resolvedFixture(t, c, project)
	if isolate.APIKey != "" {
		t.Fatal("explicit file inherited user secret")
	}
}

func TestProjectDiscoveryStopsAtGitRoot(t *testing.T) {
	c := isolatedContext(t)
	parent := filepath.Dir(c.Directory)
	putConfig(t, filepath.Join(parent, DefaultConfigName), "llm:\n  model: outside\n")
	if err := os.Mkdir(filepath.Join(c.Directory, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	root := c.Directory
	c.Directory = filepath.Join(root, "nested")
	_ = os.MkdirAll(c.Directory, 0700)
	o := resolvedFixture(t, c, "")
	if o.Model == "outside" {
		t.Fatal("escaped git boundary")
	}
	putConfig(t, filepath.Join(root, DefaultConfigName), "llm:\n  model: inside\n")
	if o = resolvedFixture(t, c, ""); o.Model != "inside" {
		t.Fatal("did not discover project from child")
	}
}

func TestProfileFieldOverridesAndProtocolFallback(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  active_profile: work\n  providers:\n    - id: work\n      provider: openai\n      base_url: https://work.example/v1\n      api_key: work-secret\n      model: work-model\n      timeout: 37\n")
	c.LookupEnv = func(k string) (string, bool) {
		if k == "ANTHROPIC_API_KEY" {
			return "unrelated-secret", true
		}
		return "", false
	}
	o := &Option{Context: c, Explicit: map[string]bool{"model": true}, LLMOptions: LLMOptions{Model: "override-model"}}
	if _, err := ResolveRuntimeConfig(o); err != nil {
		t.Fatal(err)
	}
	p := ProviderConfig(o)
	if p.Model != "override-model" || p.APIKey != "work-secret" || p.BaseURL != "https://work.example/v1" || p.Provider != "openai" {
		t.Fatalf("profile override lost fields")
	}
	if p.Timeout != 37 {
		t.Fatalf("profile timeout lost: %d", p.Timeout)
	}
	o.ActiveProfile = "missing"
	o.MarkExplicit("profile")
	if _, err := ResolveRuntimeConfig(o); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestProfileMergeAndEmptyLLMEnvironment(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  providers:\n    - id: work\n      provider: openai\n      api_key: user-secret\n      model: original\n")
	putConfig(t, filepath.Join(c.Directory, DefaultConfigName), "llm:\n  providers:\n    - id: work\n      model: project\n")
	o := resolvedFixture(t, c, "")
	if len(o.Providers) != 1 || o.Model != "project" || o.APIKey != "user-secret" {
		t.Fatal("profile fields were not merged")
	}
	path := filepath.Join(c.Directory, "empty.yaml")
	putConfig(t, path, "llm:\n  provider: openai\n  api_key: \"\"\n  model: \"\"\n")
	c.LookupEnv = func(k string) (string, bool) {
		switch k {
		case "OPENAI_API_KEY":
			return "env-secret", true
		case "OPENAI_MODEL":
			return "env-model", true
		}
		return "", false
	}
	o = resolvedFixture(t, c, path)
	if o.Model != "env-model" || o.APIKey != "env-secret" {
		t.Fatal("empty template blocked environment")
	}
}

func TestUnavailableExtensionsArePreservedAndKnownFieldsValidated(t *testing.T) {
	c := isolatedContext(t)
	path := filepath.Join(c.Directory, DefaultConfigName)
	putConfig(t, path, "extensions:\n  other-product:\n    credential: hidden-secret\n")
	o := resolvedFixture(t, c, "")
	if len(o.Extensions) != 0 || len(o.Snapshot.Diagnostics) == 0 {
		t.Fatal("unavailable extension not diagnosed")
	}
	if o.Snapshot.Document["extensions"] == nil {
		t.Fatal("unavailable section lost from file snapshot")
	}
	putConfig(t, path, "llm:\n  unknown_field: invalid\n")
	if _, err := ResolveRuntimeConfig(&Option{Context: c}); err == nil || !strings.Contains(err.Error(), "llm.unknown_field") {
		t.Fatalf("missing field diagnostic: %v", err)
	}
}

func TestStagedLayerRetainsOtherLayers(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  api_key: user-secret\n")
	path := filepath.Join(c.Directory, DefaultConfigName)
	putConfig(t, path, "llm:\n  model: old\n")
	c.Replacements = map[string][]byte{path: []byte("llm:\n  model: new\n")}
	o := resolvedFixture(t, c, "")
	if o.Model != "new" || o.APIKey != "user-secret" {
		t.Fatal("staged replacement lost layering")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "old") {
		t.Fatal("resolution mutated file")
	}
}

func TestDataDirectoryCompatibility(t *testing.T) {
	c := isolatedContext(t)
	if got := resolveDataDir("", c); got != filepath.Join(c.Home, ".cyber") {
		t.Fatalf("default: %s", got)
	}
	portable := filepath.Join(filepath.Dir(c.Executable), ".cyber")
	_ = os.Mkdir(portable, 0700)
	if got := resolveDataDir("", c); got != portable {
		t.Fatal("portable data not retained")
	}
	local := filepath.Join(c.Directory, ".cyber")
	_ = os.Mkdir(local, 0700)
	if got := resolveDataDir("", c); got != local {
		t.Fatal("current directory did not win")
	}
}

func TestPrivateAtomicWriteAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", DefaultConfigName)
	if created, err := WriteFile(path, []byte("old"), false, false); err != nil || !created {
		t.Fatal(created, err)
	}
	if created, err := WriteFile(path, []byte("unwanted"), false, false); err != nil || created {
		t.Fatal(created, err)
	}
	if _, err := WriteFile(path, []byte("new"), true, true); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	b, _ := os.ReadFile(backups[0])
	if string(b) != "old" {
		t.Fatal("backup content changed")
	}
	staged, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.tmp-*.yaml"))
	if len(staged) != 0 {
		t.Fatal("staging files leaked")
	}
}

func TestFileViewDoesNotInheritCompiledSecrets(t *testing.T) {
	withDefaults(t, func() {
		DefaultAPIKey = "compiled-secret"
		c := isolatedContext(t)
		putConfig(t, c.UserFile(), "llm:\n  model: file-model\n")
		snapshot, err := LoadSnapshot(c, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		option, err := snapshot.FileOptions(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := LLMFromOption(option).Providers[0].ApiKey; got != "" {
			t.Fatal("compiled secret entered file view")
		}
	})
}

func TestProjectProfileOverridesUserShorthand(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  provider: openai\n  model: user-model\n  api_key: user-secret\n")
	putConfig(t, filepath.Join(c.Directory, DefaultConfigName), "llm:\n  providers:\n    - id: local\n      provider: anthropic\n      model: project-model\n      api_key: project-secret\n")
	option := resolvedFixture(t, c, "")
	if option.Provider != "anthropic" || option.Model != "project-model" || option.APIKey != "project-secret" {
		t.Fatal("user shorthand overrode project profile")
	}
	fileOption, err := option.Snapshot.FileOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if LLMFromOption(fileOption).Providers[0].Model != "project-model" {
		t.Fatal("file view disagrees with runtime")
	}
}

func TestProfileValidationRejectsDuplicateID(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  providers:\n    - id: duplicate\n      model: one\n    - id: duplicate\n      model: two\n")
	if _, err := LoadSnapshot(c, "", nil); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate profile accepted: %v", err)
	}
}
