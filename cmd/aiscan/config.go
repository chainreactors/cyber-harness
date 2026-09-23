package main

import (
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

// appConfig selects the reusable capability packs in the reference
// distribution. Runtime capabilities are owned by their extensions.
type appConfig struct {
	Resolved      *cfg.Resolved
	DataDir       string
	Provider      provider.StartupConfig
	Scanner       scannerext.Config
	Tools         toolConfig
	Logger        telemetry.Logger
	CLISkillPaths []string
	SkipEngines   bool
}

type toolConfig struct {
	TavilyKeys        string
	PlaywrightSession string
	OptionalTools     []string
	MitmCapture       *bool
	TrafficStorage    cfg.TrafficOptions
}

func appConfigFromOption(option *cfg.Option, providerMode profilepkg.ProviderMode, logger telemetry.Logger) appConfig {
	dataDir := cfg.ResolveDataDir(option.DataDir)
	// Sections were resolved and validated during startup; a read error here is
	// structurally impossible, so a zero section is the safe fallback.
	hub, _ := scannerext.ReadCyberhub(option)
	recon, _ := scannerext.ReadRecon(option)
	searchKeys, _ := searchext.ReadKeys(option)
	return appConfig{
		DataDir: dataDir, Resolved: option.Resolved,
		Provider: provider.StartupConfig{
			Mode: providerMode, Config: cfg.ProviderConfig(option),
			Fallbacks: cfg.FallbackProviderConfigs(option),
		},
		Scanner: scannerext.Config{
			AgentName: "cyber",
			Resources: resources.Options{
				CyberhubURL: hub.URL, APIKey: hub.Key,
				Mode: hub.Mode, Proxy: hub.Proxy,
			},
			Recon: engine.ReconOptions{
				FofaKey: recon.FofaKey, HunterAPIKey: recon.HunterAPIKey, IngressProxy: recon.Proxy,
				Limit: intValue(recon.Limit), Credentials: scannerext.UncoverCredentials(envLookup(option)),
			},
		},
		Tools: toolConfig{
			TavilyKeys:        tavilyKeys(recon.TavilyKey, searchKeys),
			PlaywrightSession: option.PlaywrightSession, OptionalTools: option.Tools,
			MitmCapture: cloneBool(hub.Mitm), TrafficStorage: option.TrafficOptions,
		},
		Logger: logger, CLISkillPaths: skillPaths(option),
	}
}

func envLookup(option *cfg.Option) func(string) (string, bool) {
	if option.Context != nil && option.Context.LookupEnv != nil {
		return option.Context.LookupEnv
	}
	return os.LookupEnv
}

func skillPaths(option *cfg.Option) []string {
	var paths []string
	for _, value := range option.Skills {
		if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
			paths = append(paths, value)
		}
	}
	return paths
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

func intValue(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}

func cloneStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBool(source *bool) *bool {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
