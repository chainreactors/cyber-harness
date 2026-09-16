package main

import (
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resources"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

// applicationConfig is product composition input. It stays in cmd/aiscan so
// the shared App contains runtime state rather than scanner/product settings.
type applicationConfig struct {
	Resolved      *cfg.Resolved
	DataDir       string
	Provider      provider.StartupConfig
	Scanner       scannerext.Config
	Tools         applicationToolConfig
	Logger        telemetry.Logger
	CLISkillPaths []string
	SkipEngines   bool
}

type applicationToolConfig struct {
	TavilyKeys        string
	PlaywrightSession string
	OptionalTools     []string
	MitmCapture       *bool
	TrafficStorage    cfg.TrafficOptions
}

func applicationConfigFromOption(option *cfg.Option, providerMode profilepkg.ProviderMode, logger telemetry.Logger) applicationConfig {
	dataDir := cfg.ResolveDataDir(option.DataDir)
	return applicationConfig{
		DataDir: dataDir, Resolved: option.Resolved,
		Provider: provider.StartupConfig{
			Mode: providerMode, Config: app.ProviderConfig(option),
			Fallbacks: app.FallbackProviderConfigs(option),
		},
		Scanner: scannerext.Config{
			Resources: resources.Options{
				CyberhubURL: option.CyberhubURL, APIKey: option.CyberhubKey,
				Mode: option.CyberhubMode, Proxy: option.Proxy,
			},
			Recon: engine.ReconOptions{
				FofaKey: option.FofaKey, HunterAPIKey: option.HunterAPIKey, IngressProxy: option.ReconProxy,
				Limit: applicationIntValue(option.ReconLimit), Credentials: cloneApplicationStrings(option.UncoverCredentials),
			},
		},
		Tools: applicationToolConfig{
			TavilyKeys:        applicationTavilyKeys(option.TavilyKey, option.SearchConfig.TavilyKeys, cfg.DefaultTavilyKeys),
			PlaywrightSession: option.PlaywrightSession, OptionalTools: option.Tools,
			MitmCapture: cloneApplicationBool(option.Mitm), TrafficStorage: option.TrafficOptions,
		},
		Logger: logger, CLISkillPaths: applicationSkillPaths(option),
	}
}

func applicationSkillPaths(option *cfg.Option) []string {
	var paths []string
	for _, value := range option.Skills {
		if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
			paths = append(paths, value)
		}
	}
	return paths
}

func applicationTavilyKeys(primary string, fallbacks ...string) string {
	keys := make([]string, 0, len(fallbacks)+1)
	for _, raw := range append([]string{primary}, fallbacks...) {
		if raw = strings.TrimSpace(raw); raw != "" {
			keys = append(keys, raw)
		}
	}
	return strings.Join(keys, ",")
}

func applicationIntValue(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}

func cloneApplicationStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneApplicationBool(source *bool) *bool {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
