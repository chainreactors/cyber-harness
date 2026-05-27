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

