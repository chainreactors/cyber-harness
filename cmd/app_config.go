package cmd

import (
	"os"

	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type runtimeFeatures struct {
	ProviderEnabled  bool
	ProviderOptional bool
	ToolsEnabled     bool
	AIEnabled        bool
	ScannerAI        bool
	Warning          string
}

func appConfig(option *Option, features runtimeFeatures, logger telemetry.Logger) app.Config {
	return app.Config{
		Provider: app.ProviderConfig{
			Enabled:  features.ProviderEnabled,
			Config:   providerConfig(option),
			Optional: features.ProviderOptional,
		},
		Vision: app.ProviderConfig{
			Enabled: visionHasProviderConfig(option),
			Config:  visionProviderConfig(option),
		},
		Scanner: app.ScannerConfig{
			CyberhubURL:  option.CyberhubURL,
			CyberhubKey:  option.CyberhubKey,
			CyberhubMode: option.CyberhubMode,
			AIEnabled:    features.AIEnabled,
			AITimeout:    defaultInt(DefaultVerifyTimeout, 120),
			Proxy:        option.ScannerOptions.Proxy,
			FofaEmail:    resolveString(option.FofaEmail, os.Getenv("FOFA_EMAIL")),
			FofaKey:      resolveString(option.FofaKey, os.Getenv("FOFA_KEY")),
			HunterToken:  resolveString(option.HunterToken, os.Getenv("HUNTER_TOKEN")),
			HunterAPIKey: resolveString(option.HunterAPIKey, os.Getenv("HUNTER_API_KEY")),
			ReconProxy:   resolveString(option.ReconProxy, os.Getenv("RECON_PROXY")),
			ReconLimit:   intOptionValue(option.ReconLimit),
		},
		Tools: app.ToolConfig{
			Enabled:       features.ToolsEnabled,
			BashTimeout:   300,
			VisionEnabled: visionEnabled(option),
			TavilyKeys:    DefaultTavilyKeys,
		},
		Logger: logger,
	}
}
