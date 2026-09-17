package client

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"os"
	"path/filepath"
	"testing"
)

func TestClientOptionsAreExplicitAndIndependent(t *testing.T) {
	for _, test := range []struct {
		name, web string
		fields    map[string]any
		want      string
	}{
		{"absent", "", nil, ""},
		{"same origin", "https://token@example.test/base", nil, "https://token@example.test/base/ioa"},
		{"independent", "http://web.test", map[string]any{"url": "https://other@ioa.test"}, "https://other@ioa.test"},
		{"explicit disable", "http://web.test", map[string]any{"url": ""}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			option := &cfg.Option{AgentOptions: cfg.AgentOptions{ServerURL: test.web}, Extensions: cfg.Values{ConfigKey: test.fields}}
			value, err := ReadOptions(option)
			if err != nil || value.URL != test.want {
				t.Fatalf("options = %+v, %v", value, err)
			}
		})
	}
	if _, err := ReadOptions(&cfg.Option{Extensions: cfg.Values{ConfigKey: {"url": "bad"}}}); err == nil {
		t.Fatal("invalid URL silently ignored")
	}
}

func TestClientLegacyYAMLAndCLIOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("extensions:\n  ioa.client:\n    url: https://ioa.test\n    space: production\n    node_name: worker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := cfg.NewSections()
	if _, err := r.Add(Section()); err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []cfg.Values{nil, {ConfigKey: {"space": ""}}, {ConfigKey: {"space": "cli"}}} {
		fields, err := r.Resolve(path, explicit)
		if err != nil {
			t.Fatal(err)
		}
		value, err := ReadOptions(&cfg.Option{Extensions: fields})
		if err != nil {
			t.Fatal(err)
		}
		want := "production"
		if explicit != nil {
			want = explicit[ConfigKey]["space"].(string)
		}
		if value.Space != want || value.NodeName != "worker" || value.URL != "https://ioa.test" {
			t.Fatalf("legacy options = %+v", value)
		}
	}
}

func TestExtensionBuildDefaultsYieldToExplicitEmptyValues(t *testing.T) {
	url, space := DefaultURL, DefaultSpace
	DefaultURL, DefaultSpace = "https://compiled.test", "compiled"
	t.Cleanup(func() { DefaultURL, DefaultSpace = url, space })
	option := &cfg.Option{AgentOptions: cfg.AgentOptions{ServerURL: "https://web.test"}}
	value, err := ReadOptions(option)
	if err != nil || value.URL != DefaultURL || value.Space != DefaultSpace {
		t.Fatalf("compiled options = %+v, %v", value, err)
	}
	option.Extensions = cfg.Values{ConfigKey: {"url": "", "space": ""}}
	value, err = ReadOptions(option)
	if err != nil || value.URL != "" || value.Space != "" {
		t.Fatalf("explicit zero lost: %+v, %v", value, err)
	}
}
