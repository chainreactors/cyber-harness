package runner

import (
	"strings"

	"github.com/chainreactors/aiscan/agent"
	cfg "github.com/chainreactors/aiscan/core/config"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func defaultProviderConfig() agent.ProviderConfig {
	return agent.ProviderConfig{
		Provider: agent.NormalizeProvider(cfg.DefaultProvider),
		BaseURL:  cfg.DefaultBaseURL,
		APIKey:   cfg.DefaultAPIKey,
		Model:    cfg.DefaultModel,
	}
}

func hasSingleProviderFields(option *cfg.Option) bool {
	return option.Provider != "" || option.BaseURL != "" || option.APIKey != "" || option.Model != ""
}

func entryToProviderConfig(entry cfg.LLMProviderEntry) agent.ProviderConfig {
	providerName := strings.TrimSpace(entry.Provider)
	if providerName == "" {
		providerName = agent.InferProviderFromBaseURL(entry.BaseURL)
	} else {
		providerName = agent.NormalizeProvider(providerName)
	}
	cfg := agent.ProviderConfig{
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
func activeProviderIndex(option *cfg.Option) int {
	if option.ActiveProfile != "" {
		for i, entry := range option.Providers {
			if entry.ID == option.ActiveProfile {
				return i
			}
		}
	}
	return 0
}

func applyProviderLimits(providerConfig *agent.ProviderConfig, option *cfg.Option) {
	if option.MaxTokens != 0 {
		providerConfig.MaxTokens = option.MaxTokens
	}
	if option.ContextWindow != 0 {
		providerConfig.ContextWindow = option.ContextWindow
	}
}

func ProviderConfig(option *cfg.Option) agent.ProviderConfig {
	if !hasSingleProviderFields(option) && len(option.Providers) > 0 {
		cfg := entryToProviderConfig(option.Providers[activeProviderIndex(option)])
		applyProviderLimits(&cfg, option)
		return cfg
	}
	cfg := defaultProviderConfig()
	if option.Provider != "" {
		cfg.Provider = agent.NormalizeProvider(option.Provider)
	}
	if option.BaseURL != "" {
		cfg.BaseURL = option.BaseURL
		if option.Provider == "" {
			cfg.Provider = agent.InferProviderFromBaseURL(option.BaseURL)
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

func FallbackProviderConfigs(option *cfg.Option) []agent.ProviderConfig {
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

func ApplyResolvedProviderOptions(option *cfg.Option, providerConfig agent.ProviderConfig) {
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
func ProviderConfigFromProto(llm *types.LLMConfig) agent.ProviderConfig {
	active := cfg.ActiveLLMProvider(llm)
	if active == nil {
		return defaultProviderConfig()
	}
	return providerConfigFromProto(active)
}

// FallbackProviderConfigsFromProto returns every non-active profile in order.
func FallbackProviderConfigsFromProto(llm *types.LLMConfig) []agent.ProviderConfig {
	if llm == nil {
		return nil
	}
	active := cfg.ActiveLLMProvider(llm)
	var configs []agent.ProviderConfig
	for _, profile := range llm.Providers {
		if active != nil && profile.Id == active.Id {
			continue
		}
		configs = append(configs, providerConfigFromProto(profile))
	}
	return configs
}

func providerConfigFromProto(profile *types.LLMProviderConfig) agent.ProviderConfig {
	profile = cfg.NormalizeLLMProvider(profile)
	providerName := strings.TrimSpace(profile.Provider)
	if providerName == "" {
		providerName = agent.InferProviderFromBaseURL(profile.BaseUrl)
	} else {
		providerName = agent.NormalizeProvider(providerName)
	}
	return agent.ProviderConfig{
		Provider:      providerName,
		BaseURL:       profile.BaseUrl,
		APIKey:        profile.ApiKey,
		Model:         profile.Model,
		Proxy:         profile.Proxy,
		Timeout:       120,
		MaxTokens:     int(profile.MaxTokens),
		ContextWindow: int(profile.ContextWindow),
	}
}
