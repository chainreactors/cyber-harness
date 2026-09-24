package config

import (
	"path/filepath"
	"testing"
)

func TestResolveAgentTransportDerivesSameOriginEndpoints(t *testing.T) {
	option := &Option{
		AgentOptions: AgentOptions{ServerURL: "https://token@example.test/base"},
	}
	transport, err := ResolveAgentTransport(option)
	if err != nil {
		t.Fatal(err)
	}
	if transport != AgentTransportWeb {
		t.Fatalf("transport = %q, want web", transport)
	}
	if option.ServerURL != "https://token@example.test/base" {
		t.Fatalf("server endpoint = %q", option.ServerURL)
	}
}

func TestResolveAgentTransportRequiresServerURL(t *testing.T) {
	option := &Option{AgentOptions: AgentOptions{Transport: string(AgentTransportWeb)}}
	if _, err := ResolveAgentTransport(option); err == nil {
		t.Fatal("expected missing server URL to fail")
	}
}

func TestAgentNodeStartupDefersLocalLLMResolution(t *testing.T) {
	for _, transport := range []string{"auto", "web", "local", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			c := isolatedContext(t)
			putConfig(t, filepath.Join(c.Directory, ".cyber", DefaultConfigName), "agent:\n  server_url: https://server.invalid\n  transport: "+transport+"\nllm:\n  active_profile: missing\n  provider: invalid-local-provider\n")
			c.LookupEnv = func(key string) (string, bool) {
				value, ok := map[string]string{"CYBER_MODEL": "env-model", "CYBER_API_KEY": "env-key", "CYBER_PROVIDER": "invalid-env-provider"}[key]
				return value, ok
			}
			o := &Option{Context: c, Explicit: map[string]bool{"profile": true, "api-key": true},
				LLMOptions:  LLMOptions{ActiveProfile: "stale-cli-profile", APIKey: "cli-key"},
				NodeOptions: NodeOptions{NodeID: "worker"},
			}
			_, err := ResolveAgentRuntimeConfig(o)
			if transport == "local" || transport == "stdio" {
				if err == nil {
					t.Fatal("local execution bypassed LLM validation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := ProviderConfig(o)
			if p.APIKey != "" || p.Model != "" || o.ActiveProfile != "" || len(o.Providers) != 0 {
				t.Fatal("node startup retained local LLM settings")
			}
			if o.ServerURL != "https://server.invalid" || o.NodeID != "worker" || len(o.Snapshot.Diagnostics) == 0 {
				t.Fatal("node startup lost connection settings or its remote configuration notice")
			}
		})
	}
}
