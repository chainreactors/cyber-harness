package runner

import (
	"strings"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
)

type RuntimeFeatures struct {
	ProviderEnabled  bool
	ProviderOptional bool
	ToolsEnabled     bool
	AIEnabled        bool
	ScannerAI        bool
	Warning          string
}

func AppConfig(option *cfg.Option, features RuntimeFeatures, logger telemetry.Logger) ApplicationConfig {
	return ApplicationConfig{
		Provider: ApplicationProviderConfig{
			Enabled:   features.ProviderEnabled,
			Config:    ProviderConfig(option),
			Fallbacks: FallbackProviderConfigs(option),
			Optional:  features.ProviderOptional,
		},
		Scanner: ScannerConfig{
			CyberhubURL:        option.CyberhubURL,
			CyberhubKey:        option.CyberhubKey,
			CyberhubMode:       option.CyberhubMode,
			AIEnabled:          features.AIEnabled,
			VerifyMode:         cfg.ResolveString(option.ScanConfig.Verify, cfg.DefaultVerify),
			Proxy:              option.Proxy,
			FofaEmail:          option.FofaEmail,
			FofaKey:            option.FofaKey,
			HunterToken:        option.HunterToken,
			HunterAPIKey:       option.HunterAPIKey,
			ReconProxy:         option.ReconProxy,
			ReconLimit:         intOptionValue(option.ReconLimit),
			UncoverCredentials: cloneStringMap(option.UncoverCredentials),
		},
		Tools: ToolConfig{
			Enabled:           features.ToolsEnabled,
			BashTimeout:       300,
			TavilyKeys:        resolveTavilyKeys(option.TavilyKey, option.SearchConfig.TavilyKeys, cfg.DefaultTavilyKeys),
			PlaywrightSession: option.PlaywrightSession,
			OptionalTools:     option.Tools,
		},
		Logger:        logger,
		CLISkillPaths: skillPathsFromOptions(option),
	}
}

func skillPathsFromOptions(option *cfg.Option) []string {
	var paths []string
	for _, s := range option.Skills {
		if looksLikePath(s) {
			paths = append(paths, s)
		}
	}
	return paths
}

func looksLikePath(s string) bool {
	return strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".")
}

func intOptionValue(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

func resolveTavilyKeys(primary string, fallbacks ...string) string {
	keys := make([]string, 0, len(fallbacks)+1)
	for _, raw := range append([]string{primary}, fallbacks...) {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			keys = append(keys, raw)
		}
	}
	return strings.Join(keys, ",")
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
