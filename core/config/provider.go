package config

import "github.com/chainreactors/aiscan/pkg/agent"

var (
	DefaultProvider = "openai"
	DefaultBaseURL  = ""
	DefaultAPIKey   = ""
	DefaultModel    = ""

	DefaultScannerProxy = ""

	DefaultCyberhubURL  = ""
	DefaultCyberhubKey  = ""
	DefaultCyberhubMode = "merge"

	DefaultVerify        = "auto"
	DefaultVerifyTimeout = ""

	DefaultIOAURL      = ""
	DefaultIOANodeID   = ""
	DefaultIOANodeName = ""
	DefaultSpace       = ""

	DefaultTavilyKeys = ""
)

func defaultProviderConfig() agent.ProviderConfig {
	return agent.ProviderConfig{
		Provider: DefaultProvider,
		BaseURL:  DefaultBaseURL,
		APIKey:   DefaultAPIKey,
		Model:    DefaultModel,
	}
}

func hasSingleProviderFields(option *Option) bool {
	return option.Provider != "" || option.BaseURL != "" || option.APIKey != "" || option.Model != ""
}

func entryToProviderConfig(entry LLMProviderEntry) agent.ProviderConfig {
	cfg := agent.ProviderConfig{
		Provider: entry.Provider,
		BaseURL:  entry.BaseURL,
		APIKey:   entry.APIKey,
		Model:    entry.Model,
		Proxy:    entry.Proxy,
		Timeout:  entry.Timeout,
		Images:   entry.Images,
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120
	}
	return cfg
}

func ProviderConfig(option *Option) agent.ProviderConfig {
	if !hasSingleProviderFields(option) && len(option.Providers) > 0 {
		return entryToProviderConfig(option.Providers[0])
	}
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

func FallbackProviderConfigs(option *Option) []agent.ProviderConfig {
	if !hasSingleProviderFields(option) && len(option.Providers) > 1 {
		var configs []agent.ProviderConfig
		for _, entry := range option.Providers[1:] {
			configs = append(configs, entryToProviderConfig(entry))
		}
		return configs
	}
	var configs []agent.ProviderConfig
	for _, entry := range option.Providers {
		configs = append(configs, entryToProviderConfig(entry))
	}
	return configs
}

func ApplyResolvedProviderOptions(option *Option, cfg agent.ProviderConfig) {
	option.Provider = cfg.Provider
	option.BaseURL = cfg.BaseURL
	option.APIKey = cfg.APIKey
	option.Model = cfg.Model
}
