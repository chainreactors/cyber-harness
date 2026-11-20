package main

import (
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestGatewayLegacyPrecedenceAndProxySelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		file, cli cfg.Values
		env       string
		want      string
		conflict  bool
	}{
		{name: "service", file: cfg.Values{"gateway": {"fofa": map[string]any{"key": "service"}}}, want: "service"},
		{name: "legacy CLI", file: cfg.Values{"gateway": {"fofa": map[string]any{"key": "service"}}}, cli: cfg.Values{"recon": {"fofa_key": "flag"}}, want: "flag"},
		{name: "legacy environment", file: cfg.Values{"gateway": {"fofa": map[string]any{"key": "service"}}}, env: "environment", want: "environment"},
		{name: "explicit conflict", file: cfg.Values{"gateway": {"fofa": map[string]any{"key": "service"}}, "recon": {"fofa_key": "legacy"}}, conflict: true},
		{name: "named choice", file: cfg.Values{"gateway": {"main": map[string]any{"kind": "fofa", "key": "main"}, "backup": map[string]any{"kind": "fofa", "key": "backup"}}, "recon": {"service": "main", "fofa_key": "main"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) { return test.env, name == "FOFA_KEY" && test.env != "" }
			resolved, err := defaultSections().ResolveValues(test.file, test.cli, lookup)
			if err != nil {
				t.Fatal(err)
			}
			option := &cfg.Option{Resolved: resolved, Extensions: resolved.Values(), Context: &cfg.Context{LookupEnv: lookup}}
			value, err := configFromOption(option, provider.StartupDisabled, nil, telemetry.NopLogger())
			if test.conflict {
				if err == nil || !strings.Contains(err.Error(), "conflicting FOFA") {
					t.Fatalf("expected explicit conflict: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.want != "" && value.Base.Gateway["fofa"]["key"] != test.want {
				t.Fatal("legacy precedence changed")
			}
		})
	}
	resolved, err := defaultSections().ResolveValues(cfg.Values{"cyberhub": {"proxy": "office"}, "gateway": {"office": map[string]any{"kind": "clash", "url": "https://example.test/sub"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := configFromOption(&cfg.Option{Resolved: resolved, Extensions: resolved.Values()}, provider.StartupDisabled, nil, telemetry.NopLogger())
	if err != nil || value.Base.ProxyService != "office" || value.Base.Scanner.Resources.Proxy != "" {
		t.Fatalf("named proxy was treated as an upstream URL: %v", err)
	}
}
