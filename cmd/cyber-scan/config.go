package main

import (
	"fmt"
	"os"
	"strings"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/exts/gateway"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/tools/resources"
)

// appConfig selects the reusable capability packs in the reference
// distribution. Runtime capabilities are owned by their extensions.
type appConfig struct {
	Resolved          *cfg.Resolved
	DataDir           string
	Provider          provider.StartupConfig
	Scanner           scannerext.Config
	Recon             passiveconfig.ReconOptions
	ReconCredentials  map[string]string
	Gateway           cfg.Values
	ProxyService      string
	TavilyKeys        string
	PlaywrightSession string
	OptionalTools     []string
	MitmCapture       *bool
	TrafficStorage    cfg.TrafficOptions
	Logger            telemetry.Logger
	CLISkillPaths     []string
	SkipEngines       bool
}

func configFromOption(option *cfg.Option, providerMode provider.StartupMode, sessionConfig *agentsession.Config, logger telemetry.Logger) (config, error) {
	if option == nil {
		return config{}, fmt.Errorf("cyber profile option is required")
	}
	hub, err := scannerext.ReadCyberhub(option)
	if err != nil {
		return config{}, err
	}
	recon, err := passiveconfig.ReadRecon(option)
	if err != nil {
		return config{}, err
	}
	searchKeys, err := searchext.ReadKeys(option)
	if err != nil {
		return config{}, err
	}
	scanOptions, err := scannerext.ReadScan(option)
	if err != nil {
		return config{}, err
	}
	ioaConfig, err := ioaclient.ConfigFromOption(option)
	if err != nil {
		return config{}, err
	}
	services, err := gateway.Read(option)
	if err != nil {
		return config{}, err
	}
	// Legacy flags/env keep their existing precedence. Conflicting file keys
	// require an explicit choice rather than silently selecting an account.
	for name, fields := range services {
		if recon.Service != "" && name != recon.Service {
			continue
		}
		kind, _ := fields["kind"].(string)
		if kind == "" {
			kind = name
		}
		if kind != "fofa" || fields["enabled"] == false {
			continue
		}
		legacyValues := map[string]any{"key": recon.FofaKey, "proxy": recon.Proxy}
		if recon.Limit != nil {
			legacyValues["limit"] = *recon.Limit
		}
		for field, value := range legacyValues {
			legacyField := field
			if field == "key" {
				legacyField = "fofa_key"
			}
			legacy := option.Resolved.Source("extensions.recon." + legacyField)
			current := option.Resolved.Source("extensions.gateway." + name + "." + field)
			if legacy == "cli" && current != "cli" || legacy == "environment" && current != "cli" && current != "environment" {
				fields[field] = value
			} else if field == "key" && recon.FofaKey != "" {
				if key, _ := fields[field].(string); key != "" && key != recon.FofaKey && legacy == current {
					return config{}, fmt.Errorf("conflicting FOFA keys in recon and gateway.%s", name)
				}
			}
		}
	}
	proxyService := ""
	if hub.Proxy != "" && !strings.Contains(hub.Proxy, "://") {
		proxyService, hub.Proxy = hub.Proxy, ""
	}
	return config{
		Option: option, IOA: ioaConfig, Session: sessionConfig,
		Base: appConfig{
			DataDir: cfg.ResolveDataDir(option.DataDir), Resolved: option.Resolved,
			Provider: provider.StartupConfig{
				Mode: providerMode, Config: cfg.ProviderConfig(option),
				Fallbacks: cfg.FallbackProviderConfigs(option),
			},
			Scanner: scannerext.Config{
				AgentName: "cyber",
				Verify:    scanOptions.Verify,
				Resources: resources.Options{
					CyberhubURL: hub.URL, APIKey: hub.Key,
					Mode: hub.Mode, Proxy: hub.Proxy,
				},
			},
			Recon: recon, ReconCredentials: passiveconfig.UncoverCredentials(envLookup(option)),
			Gateway: services, ProxyService: proxyService,
			TavilyKeys:        tavilyKeys(recon.TavilyKey, searchKeys),
			PlaywrightSession: option.PlaywrightSession, OptionalTools: option.Tools,
			MitmCapture: hub.Mitm, TrafficStorage: option.TrafficOptions,
			Logger: logger, CLISkillPaths: cfg.LocalSkillPaths(option.Skills),
		},
	}, nil
}

func envLookup(option *cfg.Option) func(string) (string, bool) {
	if option.Context != nil && option.Context.LookupEnv != nil {
		return option.Context.LookupEnv
	}
	return os.LookupEnv
}

func tavilyKeys(primary string, fallbacks ...string) string {
	keys := make([]string, 0, len(fallbacks)+1)
	for _, raw := range append([]string{primary}, fallbacks...) {
		if raw = strings.TrimSpace(raw); raw != "" {
			keys = append(keys, raw)
		}
	}
	return strings.Join(keys, ",")
}
