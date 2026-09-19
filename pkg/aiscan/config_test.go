package aiscan

import (
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
)

func TestApplicationConfigFromOptionCarriesWideSettings(t *testing.T) {
	disabled := false
	option := &cfg.Option{
		LLMOptions: cfg.LLMOptions{Provider: "openai", Model: "gpt-4o"},
		ScannerOptions: cfg.ScannerOptions{
			CyberhubURL: "https://hub", CyberhubKey: "hub-key", CyberhubMode: "release",
			Proxy: "http://proxy", Mitm: &disabled,
		},
		ReconOptions:       cfg.ReconOptions{FofaKey: "fofa", HunterAPIKey: "hk", ReconProxy: "http://recon-proxy"},
		SearchConfig:       cfg.SearchConfigOptions{TavilyKeys: "tv-1,tv-2"},
		AgentOptions:       cfg.AgentOptions{Tools: []string{"search", "browser"}},
		PlaywrightSession:  "browser-1",
		TrafficOptions:     cfg.TrafficOptions{BodyStorage: "disk"},
		UncoverCredentials: map[string]string{"SHODAN_API_KEY": "shodan-key"},
	}
	config := appConfigFromOption(option, profilepkg.ProviderDisabled, telemetry.NopLogger())
	if config.Provider.Config.Model != "gpt-4o" || config.Provider.Mode != provider.StartupDisabled {
		t.Fatalf("provider config = %+v", config.Provider)
	}
	if config.Scanner.Resources.CyberhubURL != "https://hub" || config.Scanner.Resources.APIKey != "hub-key" || config.Scanner.Resources.Mode != "release" || config.Scanner.Resources.Proxy != "http://proxy" {
		t.Fatalf("cyberhub config = %+v", config.Scanner)
	}
	if config.Scanner.Recon.FofaKey != "fofa" || config.Scanner.Recon.HunterAPIKey != "hk" || config.Scanner.Recon.IngressProxy != "http://recon-proxy" {
		t.Fatalf("recon config = %+v", config.Scanner)
	}
	if config.Tools.TavilyKeys != "tv-1,tv-2" || len(config.Tools.OptionalTools) != 2 {
		t.Fatalf("application config = %+v", config)
	}
	if config.Tools.MitmCapture == nil || *config.Tools.MitmCapture || config.Tools.PlaywrightSession != "browser-1" || config.Tools.TrafficStorage != option.TrafficOptions {
		t.Fatalf("tool extras = %+v", config.Tools)
	}
	if config.Scanner.Recon.Credentials["SHODAN_API_KEY"] != "shodan-key" {
		t.Fatalf("scanner extras = %+v", config.Scanner)
	}
}

func TestApplicationConfigUsesCompiledDefaults(t *testing.T) {
	oldURL, oldKey, oldMode := cfg.DefaultCyberhubURL, cfg.DefaultCyberhubKey, cfg.DefaultCyberhubMode
	oldTavily := cfg.DefaultTavilyKeys
	t.Cleanup(func() {
		cfg.DefaultCyberhubURL, cfg.DefaultCyberhubKey, cfg.DefaultCyberhubMode = oldURL, oldKey, oldMode
		cfg.DefaultTavilyKeys = oldTavily
	})
	cfg.DefaultCyberhubURL = "http://hub:8080"
	cfg.DefaultCyberhubKey = "HUBKEY"
	cfg.DefaultCyberhubMode = "override"
	cfg.DefaultTavilyKeys = "BUILTIN_TAVILY"

	option := &cfg.Option{}
	cfg.ApplyDefaults(option)
	config := appConfigFromOption(option, profilepkg.ProviderOptional, telemetry.NopLogger())
	if config.Scanner.Resources.CyberhubURL != cfg.DefaultCyberhubURL || config.Scanner.Resources.APIKey != cfg.DefaultCyberhubKey || config.Scanner.Resources.Mode != cfg.DefaultCyberhubMode {
		t.Fatalf("scanner cyberhub config = %#v", config.Scanner)
	}
	if config.Tools.TavilyKeys != cfg.DefaultTavilyKeys {
		t.Fatalf("tool search config = %#v", config.Tools)
	}
	if config.Provider.Mode != provider.StartupOptional {
		t.Fatalf("provider config = %#v", config.Provider)
	}
}
