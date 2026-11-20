package config

import (
	"bytes"
	"testing"

	types "github.com/chainreactors/cyber/core/types"
)

func TestDistributeConfigUsesFlatSections(t *testing.T) {
	input := []byte("gateway:\n  fofa:\n    key: secret\n  office:\n    kind: clash\n    url: https://example.test/subscription\n    enabled: false\n")
	config, err := LoadDistributeConfigYAML(input)
	if err != nil {
		t.Fatal(err)
	}
	services := config.GetExtensions()["gateway"].AsMap()
	if services["fofa"].(map[string]any)["key"] != "secret" || services["office"].(map[string]any)["enabled"] != false {
		t.Fatalf("wire projection changed service configuration: %v", services)
	}
	output, err := MarshalDistributeConfigYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte("extensions:")) || !bytes.Contains(output, []byte("gateway:")) {
		t.Fatalf("file projection introduced a wrapper: %s", output)
	}
	if _, err := LoadDistributeConfigYAML([]byte("extensions:\n  gateway:\n    fofa:\n      key: obsolete\n")); err == nil {
		t.Fatal("transport loader accepted obsolete file format")
	}
}

func TestNormalizeLLMConfigCanonicalizesProviderProtocol(t *testing.T) {
	llm := &types.LLMConfig{Providers: []*types.LLMProviderConfig{
		{Id: "openai", Provider: " OPENAI ", BaseUrl: "https://api.deepseek.com/v1"},
		{Id: "claude", Provider: "ANTHROPIC"},
		{Id: "invalid", Provider: "deepseek"},
	}}
	NormalizeLLMConfig(llm)

	if llm.Providers[0].Provider != "openai" {
		t.Fatalf("OpenAI-compatible provider = %q", llm.Providers[0].Provider)
	}
	if llm.Providers[1].Provider != "anthropic" {
		t.Fatalf("Anthropic provider = %q", llm.Providers[1].Provider)
	}
	if llm.Providers[2].Provider != "deepseek" {
		t.Fatalf("unsupported provider must not be rewritten, got %q", llm.Providers[2].Provider)
	}
}

func TestDistributeConfigYAMLRoundTripPreservesProviderCapabilities(t *testing.T) {
	images := false
	want := &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{{
			Id: "primary", Provider: "openai", Model: "gpt-test",
			Timeout: 45, Images: &images,
		}},
	}}
	data, err := MarshalDistributeConfigYAML(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadDistributeConfigYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	profile := got.GetLlm().GetProviders()[0]
	if profile.GetTimeout() != 45 || profile.Images == nil || profile.GetImages() {
		t.Fatalf("provider capabilities lost: %+v", profile)
	}
}
