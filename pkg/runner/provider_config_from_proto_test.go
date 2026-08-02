package runner

import (
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func TestProviderConfigFromProtoSelectsActiveProfileAndFallbacks(t *testing.T) {
	llm := &types.LLMConfig{
		ActiveProfile: "openai",
		Providers: []*types.LLMProviderConfig{
			{Id: "deepseek", Provider: "openai", ApiKey: "dk-111", Model: "deepseek-chat", MaxTokens: 8192},
			{Id: "openai", Provider: "openai", ApiKey: "sk-222", Model: "gpt-4o", MaxTokens: 32768},
		},
	}
	primary := ProviderConfigFromProto(llm)
	if primary.Provider != "openai" || primary.APIKey != "sk-222" || primary.MaxTokens != 32768 {
		t.Fatalf("primary profile = %+v", primary)
	}
	fallbacks := FallbackProviderConfigsFromProto(llm)
	if len(fallbacks) != 1 || fallbacks[0].Provider != "openai" || fallbacks[0].APIKey != "dk-111" {
		t.Fatalf("fallback profiles = %+v", fallbacks)
	}
}

func TestProviderConfigFromProtoInfersProtocolFromBaseURL(t *testing.T) {
	llm := &types.LLMConfig{Providers: []*types.LLMProviderConfig{
		{Id: "claude", BaseUrl: "https://api.anthropic.com", ApiKey: "ak", Model: "claude-opus-4-7"},
	}}
	primary := ProviderConfigFromProto(llm)
	if primary.Provider != "anthropic" {
		t.Fatalf("inferred provider = %q, want anthropic", primary.Provider)
	}
}

func TestAppConfigFromDistributeMapsProtoSections(t *testing.T) {
	dc := &types.DistributeConfig{
		Llm: &types.LLMConfig{
			ActiveProfile: "main",
			Providers:     []*types.LLMProviderConfig{{Id: "main", Provider: "openai", ApiKey: "sk", Model: "gpt-4o"}},
		},
		Cyberhub: &types.CyberhubConfig{Url: "https://hub", Key: "hub-key", Mode: "release", Proxy: "http://proxy"},
		Recon: &types.ReconConfig{
			FofaEmail: "a@b.c", FofaKey: "fofa", HunterToken: "ht", HunterApiKey: "hk",
			Proxy: "http://recon-proxy", Limit: 42,
		},
		Scan:   &types.ScanConfig{Verify: "high"},
		Search: &types.SearchConfig{TavilyKeys: "tv-1,tv-2"},
		Agent:  &types.AgentConfig{Tools: []string{"search", "browser"}},
	}
	rc := AppConfigFromDistribute(dc, RuntimeFeatures{ProviderEnabled: true, ToolsEnabled: true, AIEnabled: true}, telemetry.NopLogger())

	if rc.Provider.Config.Model != "gpt-4o" || !rc.Provider.Enabled {
		t.Fatalf("provider config = %+v", rc.Provider)
	}
	if rc.Scanner.CyberhubURL != "https://hub" || rc.Scanner.CyberhubKey != "hub-key" || rc.Scanner.CyberhubMode != "release" || rc.Scanner.Proxy != "http://proxy" {
		t.Fatalf("cyberhub = %+v", rc.Scanner)
	}
	if rc.Scanner.FofaEmail != "a@b.c" || rc.Scanner.FofaKey != "fofa" || rc.Scanner.HunterToken != "ht" || rc.Scanner.HunterAPIKey != "hk" {
		t.Fatalf("recon = %+v", rc.Scanner)
	}
	if rc.Scanner.ReconProxy != "http://recon-proxy" || rc.Scanner.ReconLimit != 42 {
		t.Fatalf("recon proxy/limit = %+v", rc.Scanner)
	}
	if rc.Scanner.VerifyMode != "high" || !rc.Scanner.AIEnabled {
		t.Fatalf("scan section = %+v", rc.Scanner)
	}
	if rc.Tools.TavilyKeys != "tv-1,tv-2" || len(rc.Tools.OptionalTools) != 2 || !rc.Tools.Enabled {
		t.Fatalf("tools = %+v", rc.Tools)
	}
}

func TestMergeOptionExtrasLayersNonProtoFields(t *testing.T) {
	rc := AppConfigFromDistribute(&types.DistributeConfig{}, RuntimeFeatures{}, telemetry.NopLogger())
	option := &cfg.Option{
		PlaywrightSession:  "browser-1",
		UncoverCredentials: map[string]string{"SHODAN_API_KEY": "shodan-key"},
	}
	rc = MergeOptionExtras(rc, option)
	if rc.Tools.PlaywrightSession != "browser-1" || rc.Scanner.UncoverCredentials["SHODAN_API_KEY"] != "shodan-key" {
		t.Fatalf("extras = %+v", rc)
	}
}
