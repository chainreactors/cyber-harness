package aiscan

import (
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	app "github.com/chainreactors/cyber/pkg/app"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

// appConfig selects the reusable capability packs in the reference
// distribution. Shared runtime state remains in app.State.
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
	return appConfig{
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
				Limit: intValue(option.ReconLimit), Credentials: cloneStrings(option.UncoverCredentials),
			},
		},
		Tools: toolConfig{
			TavilyKeys:        tavilyKeys(option.TavilyKey, option.SearchConfig.TavilyKeys, cfg.DefaultTavilyKeys),
			PlaywrightSession: option.PlaywrightSession, OptionalTools: option.Tools,
			MitmCapture: cloneBool(option.Mitm), TrafficStorage: option.TrafficOptions,
		},
		Logger: logger, CLISkillPaths: skillPaths(option),
	}
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
