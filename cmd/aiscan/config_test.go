package main

import (
	"testing"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/telemetry"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestApplicationConfigFromOptionCarriesWideSettings(t *testing.T) {
	t.Setenv("SHODAN_API_KEY", "shodan-key")
	option := &cfg.Option{
		LLMOptions: cfg.LLMOptions{Provider: "openai", Model: "gpt-4o"},
		Extensions: cfg.Values{
			scannerext.CyberhubConfigKey: {
				"url": "https://hub", "key": "hub-key", "mode": "release",
				"proxy": "http://proxy", "mitm": false,
			},
			passiveconfig.ReconConfigKey: {"fofa_key": "fofa", "hunter_api_key": "hk", "proxy": "http://recon-proxy"},
			searchext.ConfigKey:          {"tavily_keys": "tv-1,tv-2"},
		},
		AgentOptions:      cfg.AgentOptions{Tools: []string{"search", "browser"}},
		PlaywrightSession: "browser-1",
		TrafficOptions:    cfg.TrafficOptions{BodyStorage: "disk"},
	}
	profile, err := configFromOption(option, provider.StartupDisabled, nil, telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	config := profile.Base
	if config.Provider.Config.Model != "gpt-4o" || config.Provider.Mode != provider.StartupDisabled {
		t.Fatalf("provider config = %+v", config.Provider)
	}
	if config.Scanner.Resources.CyberhubURL != "https://hub" || config.Scanner.Resources.APIKey != "hub-key" || config.Scanner.Resources.Mode != "release" || config.Scanner.Resources.Proxy != "http://proxy" {
		t.Fatalf("cyberhub config = %+v", config.Scanner)
	}
	if config.Recon.FofaKey != "fofa" || config.Recon.HunterAPIKey != "hk" || config.Recon.Proxy != "http://recon-proxy" {
		t.Fatalf("recon config = %+v", config.Scanner)
	}
	if config.TavilyKeys != "tv-1,tv-2" || len(config.OptionalTools) != 2 {
		t.Fatalf("application config = %+v", config)
	}
	if config.MitmCapture == nil || *config.MitmCapture || config.PlaywrightSession != "browser-1" || config.TrafficStorage != option.TrafficOptions {
		t.Fatalf("tool extras = %+v", config)
	}
	if config.ReconCredentials["SHODAN_API_KEY"] != "shodan-key" {
		t.Fatalf("scanner extras = %+v", config.Scanner)
	}
}

func TestApplicationConfigUsesCompiledDefaults(t *testing.T) {
	oldURL, oldKey, oldMode := scannerext.DefaultCyberhubURL, scannerext.DefaultCyberhubKey, scannerext.DefaultCyberhubMode
	oldTavily := searchext.DefaultTavilyKeys
	t.Cleanup(func() {
		scannerext.DefaultCyberhubURL, scannerext.DefaultCyberhubKey, scannerext.DefaultCyberhubMode = oldURL, oldKey, oldMode
		searchext.DefaultTavilyKeys = oldTavily
	})
	scannerext.DefaultCyberhubURL = "http://hub:8080"
	scannerext.DefaultCyberhubKey = "HUBKEY"
	scannerext.DefaultCyberhubMode = "override"
	searchext.DefaultTavilyKeys = "BUILTIN_TAVILY"

	option := &cfg.Option{}
	cfg.ApplyDefaults(option)
	profile, err := configFromOption(option, provider.StartupOptional, nil, telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	config := profile.Base
	if config.Scanner.Resources.CyberhubURL != scannerext.DefaultCyberhubURL || config.Scanner.Resources.APIKey != scannerext.DefaultCyberhubKey || config.Scanner.Resources.Mode != scannerext.DefaultCyberhubMode {
		t.Fatalf("scanner cyberhub config = %#v", config.Scanner)
	}
	if config.TavilyKeys != searchext.DefaultTavilyKeys {
		t.Fatalf("tool search config = %#v", config)
	}
	if config.Provider.Mode != provider.StartupOptional {
		t.Fatalf("provider config = %#v", config.Provider)
	}
}

func TestProfileConfigRejectsInvalidExtensionSettings(t *testing.T) {
	for _, section := range []string{scannerext.CyberhubConfigKey, passiveconfig.ReconConfigKey, searchext.ConfigKey, scannerext.ScanConfigKey} {
		t.Run(section, func(t *testing.T) {
			option := &cfg.Option{Extensions: cfg.Values{section: {"retired_field": true}}}
			if _, err := configFromOption(option, provider.StartupDisabled, nil, telemetry.NopLogger()); err == nil {
				t.Fatal("invalid extension settings were replaced with zero values")
			}
		})
	}
}
