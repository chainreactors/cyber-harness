package scanner

import (
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func resolveSections(t *testing.T, file, cli cfg.Values, env map[string]string) *cfg.Resolved {
	t.Helper()
	registry := cfg.NewSections()
	if _, err := registry.Add(CyberhubSection(), ScanSection()); err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.ResolveValues(file, cli, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestCyberhubSectionPrecedence(t *testing.T) {
	resolved := resolveSections(t,
		cfg.Values{CyberhubConfigKey: map[string]any{"url": "http://file"}},
		cfg.Values{CyberhubConfigKey: map[string]any{"mode": "override"}},
		map[string]string{"CYBER_CYBERHUB_URL": "http://env", "CYBER_CYBERHUB_KEY": "env-key"},
	)
	value, err := cfg.Get[*CyberhubOptions](resolved, CyberhubConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	// CLI beats environment, environment beats file, defaults fill the rest.
	if value.URL != "http://env" {
		t.Fatalf("url = %q, want env override", value.URL)
	}
	if value.Key != "env-key" || value.Mode != "override" {
		t.Fatalf("key/mode = %q/%q", value.Key, value.Mode)
	}
	if value.Proxy != DefaultScannerProxy || value.Mitm != nil {
		t.Fatalf("defaults = %#v", value)
	}
}

func TestCyberhubSectionDefaultsFollowBuildVariables(t *testing.T) {
	t.Cleanup(SetDefaults(CyberhubOptions{URL: "http://build", Key: DefaultCyberhubKey, Mode: DefaultCyberhubMode, Proxy: DefaultScannerProxy}))
	resolved := resolveSections(t, nil, nil, nil)
	value, err := cfg.Get[*CyberhubOptions](resolved, CyberhubConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.URL != "http://build" {
		t.Fatalf("url = %q, want build-time default", value.URL)
	}
	if value.Mode != "merge" {
		t.Fatalf("mode = %q, want merge", value.Mode)
	}
}

func TestScanSectionVerifyStaysEmptyUnlessConfigured(t *testing.T) {
	resolved := resolveSections(t, nil, nil, nil)
	value, err := cfg.Get[*ScanOptions](resolved, ScanConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.Verify != "" {
		t.Fatalf("verify = %q, want empty until the execution node resolves it", value.Verify)
	}
	resolved = resolveSections(t, cfg.Values{ScanConfigKey: map[string]any{"verify": "on"}}, nil, nil)
	value, _ = cfg.Get[*ScanOptions](resolved, ScanConfigKey)
	if value.Verify != "on" {
		t.Fatalf("verify = %q", value.Verify)
	}
}

func TestCyberhubSectionPreservesExplicitMitmFalse(t *testing.T) {
	resolved := resolveSections(t, cfg.Values{CyberhubConfigKey: map[string]any{"mitm": false}}, nil, nil)
	value, err := cfg.Get[*CyberhubOptions](resolved, CyberhubConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.Mitm == nil || *value.Mitm {
		t.Fatalf("explicit mitm:false lost: %#v", value.Mitm)
	}
}
