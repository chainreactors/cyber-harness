package config

import "testing"

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
