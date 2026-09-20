package config

import (
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
)

func defaultProviderConfig() provider.ProviderConfig {
	return provider.ProviderConfig{
		Provider: provider.NormalizeProvider(DefaultProvider),
		BaseURL:  DefaultBaseURL,
		APIKey:   DefaultAPIKey,
		Model:    DefaultModel,
	}
}

// HasSingleProviderFields reports whether the flat single-provider flags win
// over the profile list, which is how the runtime resolves the active provider.
func HasSingleProviderFields(option *Option) bool {
	return option.Provider != "" || option.BaseURL != "" || option.APIKey != "" || option.Model != ""
}

func entryToProviderConfig(entry LLMProviderEntry) provider.ProviderConfig {
	providerName := strings.TrimSpace(entry.Provider)
	if providerName == "" {
		providerName = provider.InferFromBaseURL(entry.BaseURL)
	} else {
		providerName = provider.NormalizeProvider(providerName)
	}
	cfg := provider.ProviderConfig{
		Provider:      providerName,
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

func applyProviderLimits(providerConfig *provider.ProviderConfig, option *Option) {
	if option.MaxTokens != 0 {
		providerConfig.MaxTokens = option.MaxTokens
	}
	if option.ContextWindow != 0 {
		providerConfig.ContextWindow = option.ContextWindow
	}
}

func ProviderConfig(option *Option) provider.ProviderConfig {
	if !HasSingleProviderFields(option) && len(option.Providers) > 0 {
		cfg := entryToProviderConfig(option.Providers[activeProviderIndex(option)])
		applyProviderLimits(&cfg, option)
		return cfg
	}
	cfg := defaultProviderConfig()
	if option.Provider != "" {
		cfg.Provider = provider.NormalizeProvider(option.Provider)
	}
	if option.BaseURL != "" {
		cfg.BaseURL = option.BaseURL
		if option.Provider == "" {
			cfg.Provider = provider.InferFromBaseURL(option.BaseURL)
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

func FallbackProviderConfigs(option *Option) []provider.ProviderConfig {
	if !HasSingleProviderFields(option) && len(option.Providers) > 0 {
		active := activeProviderIndex(option)
		var configs []provider.ProviderConfig
		for i, entry := range option.Providers {
			if i == active {
				continue
			}
			configs = append(configs, entryToProviderConfig(entry))
		}
		return configs
	}
	var configs []provider.ProviderConfig
	for _, entry := range option.Providers {
		configs = append(configs, entryToProviderConfig(entry))
	}
	return configs
}

func ApplyResolvedProviderOptions(option *Option, providerConfig provider.ProviderConfig) {
	option.Provider = providerConfig.Provider
	option.BaseURL = providerConfig.BaseURL
	option.APIKey = providerConfig.APIKey
	option.Model = providerConfig.Model
	option.MaxTokens = providerConfig.MaxTokens
	option.ContextWindow = providerConfig.ContextWindow
}

// ProviderConfigFromProto resolves the active LLM profile directly from the
// canonical config proto. This is the only provider-config path used when a
// DistributeConfig is already in hand (remote agents, hub reload).
func ProviderConfigFromProto(llm *types.LLMConfig) provider.ProviderConfig {
	active := ActiveLLMProvider(llm)
	if active == nil {
		return defaultProviderConfig()
	}
	return providerConfigFromProto(active)
}

// FallbackProviderConfigsFromProto returns every non-active profile in order.
func FallbackProviderConfigsFromProto(llm *types.LLMConfig) []provider.ProviderConfig {
	if llm == nil {
		return nil
	}
	active := ActiveLLMProvider(llm)
	var configs []provider.ProviderConfig
	for _, profile := range llm.Providers {
		if active != nil && profile.Id == active.Id {
			continue
		}
		configs = append(configs, providerConfigFromProto(profile))
	}
	return configs
}

func providerConfigFromProto(profile *types.LLMProviderConfig) provider.ProviderConfig {
	profile = NormalizeLLMProvider(profile)
	if profile == nil {
		return defaultProviderConfig()
	}
	providerName := strings.TrimSpace(profile.Provider)
	if providerName == "" {
		providerName = provider.InferFromBaseURL(profile.BaseUrl)
	} else {
		providerName = provider.NormalizeProvider(providerName)
	}
	result := provider.ProviderConfig{
		Provider:      providerName,
		BaseURL:       profile.BaseUrl,
		APIKey:        profile.ApiKey,
		Model:         profile.Model,
		Proxy:         profile.Proxy,
		Timeout:       int(profile.Timeout),
		Images:        profile.Images,
		MaxTokens:     int(profile.MaxTokens),
		ContextWindow: int(profile.ContextWindow),
	}
	if result.Timeout <= 0 {
		result.Timeout = 120
	}
	return result
}
