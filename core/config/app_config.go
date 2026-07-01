package config

import (
	"strings"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type RuntimeFeatures struct {
	ProviderEnabled  bool
	ProviderOptional bool
	ToolsEnabled     bool
	AIEnabled        bool
	ScannerAI        bool
	Warning          string
}

func AppConfig(option *Option, features RuntimeFeatures, logger telemetry.Logger) RuntimeConfig {
	return RuntimeConfig{
		Provider: RuntimeProviderConfig{
			Enabled:   features.ProviderEnabled,
			Config:    ProviderConfig(option),
			Fallbacks: FallbackProviderConfigs(option),
			Optional:  features.ProviderOptional,
		},
		Scanner: ScannerConfig{
			CyberhubURL:       option.CyberhubURL,
			CyberhubKey:       option.CyberhubKey,
			CyberhubMode:      option.CyberhubMode,
			AIEnabled:         features.AIEnabled,
			EnableAllAISkills: option.AI,
			AITimeout:         DefaultInt(DefaultVerifyTimeout, 120),
			VerifyMode:        DefaultVerify,
			Proxy:             option.Proxy,
			FofaEmail:         option.FofaEmail,
			FofaKey:           option.FofaKey,
			HunterToken:       option.HunterToken,
			HunterAPIKey:      option.HunterAPIKey,
			ReconProxy:        option.ReconProxy,
			ReconLimit:        intOptionValue(option.ReconLimit),
		},
		Tools: ToolConfig{
			Enabled:       features.ToolsEnabled,
			BashTimeout:   300,
			TavilyKeys:    resolveTavilyKeys(option.TavilyKey, DefaultTavilyKeys),
			OptionalTools: option.Tools,
		},
		Logger:        logger,
		CLISkillPaths: skillPathsFromOptions(option),
	}
}

func skillPathsFromOptions(option *Option) []string {
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

func resolveTavilyKeys(flagKey, configKeys string) string {
	flagKey = strings.TrimSpace(flagKey)
	configKeys = strings.TrimSpace(configKeys)
	if flagKey != "" && configKeys != "" {
		return flagKey + "," + configKeys
	}
	if flagKey != "" {
		return flagKey
	}
	return configKeys
}
