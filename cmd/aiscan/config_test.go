package main

import (
	"reflect"
	"strings"
	"testing"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/telemetry"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestApplicationConfigMatchesRawAndResolvedSections(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	for _, test := range []struct {
		name   string
		fields cfg.Values
	}{
		{name: "defaults"},
		{name: "explicit empty and zero", fields: cfg.Values{
			scannerext.CyberhubConfigKey: {"url": "", "key": "", "mitm": false},
			passiveconfig.ReconConfigKey: {"fofa_key": "", "limit": 0},
			searchext.ConfigKey:          {"tavily_keys": ""},
			scannerext.ScanConfigKey:     {"verify": "off"},
			ioaclient.ConfigKey:          {"url": "", "space": ""},
		}},
		{name: "configured", fields: cfg.Values{
			scannerext.CyberhubConfigKey: {"url": "https://hub.test", "key": "hub-key", "mode": "override", "mitm": true},
			passiveconfig.ReconConfigKey: {"fofa_key": "fofa", "tavily_key": "recon-search", "limit": 42},
			searchext.ConfigKey:          {"tavily_keys": "search-key"},
			scannerext.ScanConfigKey:     {"verify": "on"},
			ioaclient.ConfigKey:          {"url": "https://ioa.test", "space": "team", "node_name": "worker"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			option := &cfg.Option{
				Extensions: cfg.CloneValues(test.fields), Context: &cfg.Context{LookupEnv: lookup},
				NodeOptions: cfg.NodeOptions{NodeName: "fixture"},
			}
			logger := telemetry.NopLogger()
			raw, err := configFromOption(option, provider.StartupDisabled, nil, logger)
			if err != nil {
				t.Fatal(err)
			}
			option.Resolved, err = defaultSections().ResolveValues(test.fields, nil, lookup)
			if err != nil {
				t.Fatal(err)
			}
			option.Extensions = option.Resolved.Values()
			resolved, err := configFromOption(option, provider.StartupDisabled, nil, logger)
			if err != nil {
				t.Fatal(err)
			}
			// The snapshot records how configuration was evaluated; compare the
			// runtime settings that both read paths must produce.
			raw.Base.Resolved = resolved.Base.Resolved
			if !reflect.DeepEqual(raw.Base, resolved.Base) {
				t.Fatalf("raw and resolved application settings differ:\nraw: %+v\nresolved: %+v", raw.Base, resolved.Base)
			}
			if raw.IOA.URL != resolved.IOA.URL || raw.IOA.Space != resolved.IOA.Space || raw.IOA.NodeName != resolved.IOA.NodeName {
				t.Fatalf("raw and resolved IOA settings differ: raw=%+v resolved=%+v", raw.IOA, resolved.IOA)
			}
			if resolved.Base.MitmCapture != nil {
				original := *resolved.Base.MitmCapture
				*resolved.Base.MitmCapture = !original
				hub, err := scannerext.ReadCyberhub(option)
				if err != nil || hub.Mitm == nil || *hub.Mitm != original {
					t.Fatalf("runtime mutation changed the resolved MITM setting: value=%+v err=%v", hub, err)
				}
			}
			if resolved.Base.Recon.Limit != nil {
				original := *resolved.Base.Recon.Limit
				*resolved.Base.Recon.Limit = original + 1
				recon, err := passiveconfig.ReadRecon(option)
				if err != nil || recon.Limit == nil || *recon.Limit != original {
					t.Fatalf("runtime mutation changed the resolved recon limit: value=%+v err=%v", recon, err)
				}
			}
		})
	}
}

func TestApplicationConfigRejectsInvalidSectionValues(t *testing.T) {
	for _, test := range []struct {
		section string
		fields  map[string]any
	}{
		{scannerext.ScanConfigKey, map[string]any{"verify": "invalid"}},
		{scannerext.CyberhubConfigKey, map[string]any{"unknown": "value"}},
		{passiveconfig.ReconConfigKey, map[string]any{"limit": 1.5}},
		{searchext.ConfigKey, map[string]any{"unknown": "value"}},
		{ioaclient.ConfigKey, map[string]any{"url": "invalid"}},
	} {
		t.Run(test.section, func(t *testing.T) {
			option := &cfg.Option{Extensions: cfg.Values{test.section: test.fields}}
			if _, err := configFromOption(option, provider.StartupDisabled, nil, telemetry.NopLogger()); err == nil || !strings.Contains(err.Error(), test.section) {
				t.Fatalf("invalid section %s was accepted: %v", test.section, err)
			}
		})
	}
}

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
	t.Cleanup(scannerext.SetDefaults(scannerext.CyberhubOptions{URL: "http://hub:8080", Key: "HUBKEY", Mode: "override", Proxy: scannerext.DefaultScannerProxy}))
	t.Cleanup(searchext.SetDefaultKeys("BUILTIN_TAVILY"))

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
