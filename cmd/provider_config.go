package cmd

import "github.com/chainreactors/aiscan/pkg/agent/provider"

func defaultProviderConfig() provider.ProviderConfig {
	return provider.ProviderConfig{
		Provider: DefaultProvider,
		BaseURL:  DefaultBaseURL,
		APIKey:   DefaultAPIKey,
		Model:    DefaultModel,
	}
}

func providerConfig(option *Option) provider.ProviderConfig {
	cfg := defaultProviderConfig()
	if option.Provider != "" {
		cfg.Provider = option.Provider
	}
	if option.BaseURL != "" {
		cfg.BaseURL = option.BaseURL
		if option.Provider == "" {
			cfg.Provider = ""
		}
	}
	if option.APIKey != "" {
		cfg.APIKey = option.APIKey
	}
	if option.Model != "" {
		cfg.Model = option.Model
	}
	if option.LLMProxy != "" {
		cfg.Proxy = option.LLMProxy
	}
	cfg.Timeout = 120
	return cfg
}

func applyResolvedProviderOptions(option *Option, cfg provider.ProviderConfig) {
	option.Provider = cfg.Provider
	option.BaseURL = cfg.BaseURL
	option.APIKey = cfg.APIKey
	option.Model = cfg.Model
}

func visionEnabled(option *Option) bool {
	return option.Vision || visionHasProviderConfig(option)
}

func visionHasProviderConfig(option *Option) bool {
	return option.VisionProvider != "" ||
		option.VisionBaseURL != "" ||
		option.VisionAPIKey != "" ||
		option.VisionModel != "" ||
		option.VisionProxy != ""
}

func visionProviderConfig(option *Option) provider.ProviderConfig {
	cfg := provider.ProviderConfig{}
	if option.VisionProvider != "" {
		cfg.Provider = option.VisionProvider
	}
	if option.VisionBaseURL != "" {
		cfg.BaseURL = option.VisionBaseURL
	}
	if option.VisionAPIKey != "" {
		cfg.APIKey = option.VisionAPIKey
	}
	if option.VisionModel != "" {
		cfg.Model = option.VisionModel
	}
	cfg.Proxy = resolveString(option.VisionProxy, option.LLMProxy)
	cfg.Timeout = 120
	return cfg
}
