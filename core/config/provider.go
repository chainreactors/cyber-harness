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

	DefaultVerify = "auto"

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
		Provider:      entry.Provider,
		BaseURL:       entry.BaseURL,
		APIKey:        entry.APIKey,
		Model:         entry.Model,
		Proxy:         entry.Proxy,
		Timeout:       entry.Timeout,
		Images:        entry.Images,
		MaxTokens:     entry.MaxTokens,
		ContextWindow: entry.ContextWindow,
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120
	}
	return cfg
}

// activeProviderIndex resolves the primary provider profile by ActiveProfile
// id; list position is meaningless, so an unset or unknown id selects index 0.
func activeProviderIndex(option *Option) int {
	if option.ActiveProfile != "" {
		for i, entry := range option.Providers {
			if entry.ID == option.ActiveProfile {
				return i
			}
		}
	}
	return 0
}

func applyProviderLimits(cfg *agent.ProviderConfig, option *Option) {
	if option.MaxTokens != 0 {
		cfg.MaxTokens = option.MaxTokens
	}
	if option.ContextWindow != 0 {
		cfg.ContextWindow = option.ContextWindow
	}
}

func ProviderConfig(option *Option) agent.ProviderConfig {
	if !hasSingleProviderFields(option) && len(option.Providers) > 0 {
		cfg := entryToProviderConfig(option.Providers[activeProviderIndex(option)])
		applyProviderLimits(&cfg, option)
		return cfg
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
	applyProviderLimits(&cfg, option)
	cfg.Timeout = 120
	return cfg
}

func FallbackProviderConfigs(option *Option) []agent.ProviderConfig {
	if !hasSingleProviderFields(option) && len(option.Providers) > 0 {
		active := activeProviderIndex(option)
		var configs []agent.ProviderConfig
		for i, entry := range option.Providers {
			if i == active {
				continue
			}
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
	option.MaxTokens = cfg.MaxTokens
	option.ContextWindow = cfg.ContextWindow
}
