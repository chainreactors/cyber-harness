package main

import (
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestGatewayConfigAndProxySelection(t *testing.T) {
	resolved, err := defaultSections().ResolveValues(cfg.Values{"gateway": {"fofa": map[string]any{"key": "file"}}}, cfg.Values{"gateway": {"fofa": map[string]any{"key": "explicit"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := configFromOption(&cfg.Option{Resolved: resolved, Extensions: resolved.Values()}, provider.StartupDisabled, nil, telemetry.NopLogger())
	if err != nil || configured.Base.Gateway["fofa"]["key"] != "explicit" {
		t.Fatalf("new configuration precedence: %v", err)
	}
	resolved, err = defaultSections().ResolveValues(cfg.Values{"cyberhub": {"proxy": "office"}, "gateway": {"office": map[string]any{"kind": "clash", "url": "https://example.test/sub"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := configFromOption(&cfg.Option{Resolved: resolved, Extensions: resolved.Values()}, provider.StartupDisabled, nil, telemetry.NopLogger())
	if err != nil || value.Base.ProxyService != "office" || value.Base.Scanner.Resources.Proxy != "" {
		t.Fatalf("named proxy was treated as an upstream URL: %v", err)
	}
}
