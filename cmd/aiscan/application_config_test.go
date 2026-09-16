package main

import (
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	types "github.com/chainreactors/cyber/pkg/types"
)

func TestApplicationConfigFromDistribute(t *testing.T) {
	distributed := &types.DistributeConfig{
		Llm: &types.LLMConfig{ActiveProfile: "main", Providers: []*types.LLMProviderConfig{
			{Id: "main", Provider: "openai", ApiKey: "sk", Model: "gpt-4o"},
		}},
		Cyberhub: &types.CyberhubConfig{Url: "https://hub", Key: "hub-key", Mode: "release", Proxy: "http://proxy"},
		Recon:    &types.ReconConfig{FofaKey: "fofa", HunterApiKey: "hk", Proxy: "http://recon-proxy", Limit: 42},
		Search:   &types.SearchConfig{TavilyKeys: "tv-1,tv-2"},
		Agent:    &types.AgentConfig{Tools: []string{"search", "browser"}},
	}
	config := applicationConfigFromDistribute(distributed, profilepkg.ProviderRequired, telemetry.NopLogger())
	if config.Provider.Config.Model != "gpt-4o" || config.Provider.Mode != provider.StartupRequired {
		t.Fatalf("provider config = %+v", config.Provider)
	}
	if config.Scanner.Resources.CyberhubURL != "https://hub" || config.Scanner.Resources.APIKey != "hub-key" || config.Scanner.Resources.Mode != "release" || config.Scanner.Resources.Proxy != "http://proxy" {
		t.Fatalf("cyberhub config = %+v", config.Scanner)
	}
	if config.Scanner.Recon.FofaKey != "fofa" || config.Scanner.Recon.HunterAPIKey != "hk" || config.Scanner.Recon.IngressProxy != "http://recon-proxy" || config.Scanner.Recon.Limit != 42 {
		t.Fatalf("recon config = %+v", config.Scanner)
	}
	if config.Tools.TavilyKeys != "tv-1,tv-2" || len(config.Tools.OptionalTools) != 2 {
		t.Fatalf("application config = %+v", config)
	}
}

func TestApplicationConfigPreservesOptionOnlyValues(t *testing.T) {
	disabled := false
	option := &cfg.Option{
		ScannerOptions: cfg.ScannerOptions{Mitm: &disabled}, PlaywrightSession: "browser-1", TrafficOptions: cfg.TrafficOptions{BodyStorage: "disk"},
		UncoverCredentials: map[string]string{"SHODAN_API_KEY": "shodan-key"},
	}
	direct := applicationConfigFromOption(option, profilepkg.ProviderDisabled, telemetry.NopLogger())
	merged := mergeApplicationOptionExtras(applicationConfigFromDistribute(&types.DistributeConfig{}, profilepkg.ProviderDisabled, nil), option)
	for _, config := range []applicationConfig{direct, merged} {
		if config.Tools.MitmCapture == nil || *config.Tools.MitmCapture || config.Tools.PlaywrightSession != "browser-1" || config.Tools.TrafficStorage != option.TrafficOptions {
			t.Fatalf("tool extras = %+v", config.Tools)
		}
		if config.Scanner.Recon.Credentials["SHODAN_API_KEY"] != "shodan-key" {
			t.Fatalf("scanner extras = %+v", config.Scanner)
		}
	}
}
