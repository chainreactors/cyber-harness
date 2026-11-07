package main

import (
	"fmt"
	"os"
	"strings"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
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
