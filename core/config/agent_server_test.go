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
	if option.ServerURL != "https://token@example.test/base" || option.IOAURL != "https://token@example.test/base/ioa" {
		t.Fatalf("resolved endpoints = server %q ioa %q", option.ServerURL, option.IOAURL)
	}
}

func TestResolveAgentTransportKeepsIndependentIOAURL(t *testing.T) {
	option := &Option{
		AgentOptions: AgentOptions{ServerURL: "http://web-token@127.0.0.1:8080"},
		IOAOptions:   IOAOptions{IOAURL: "https://ioa-token@ioa.example/api"},
	}
	if _, err := ResolveAgentTransport(option); err != nil {
		t.Fatal(err)
	}
	if option.IOAURL != "https://ioa-token@ioa.example/api" {
		t.Fatalf("IOAURL = %q", option.IOAURL)
	}
}

func TestResolveAgentTransportRequiresServerURL(t *testing.T) {
	option := &Option{AgentOptions: AgentOptions{Transport: string(AgentTransportWeb)}}
	if _, err := ResolveAgentTransport(option); err == nil {
		t.Fatal("expected missing server URL to fail")
	}
}
