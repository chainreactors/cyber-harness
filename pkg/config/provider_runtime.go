package config

import (
	"fmt"
	"reflect"
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

// HasSingleProviderFields reports whether flat per-field overrides are present.
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

// activeProviderIndex selects a validated profile; an unset selector uses the first entry.
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
	if option.MaxTokens != 0 || option.hasExplicit("MaxTokens") {
		providerConfig.MaxTokens = option.MaxTokens
	}
	if option.ContextWindow != 0 || option.hasExplicit("ContextWindow") {
		providerConfig.ContextWindow = option.ContextWindow
	}
}

func ProviderConfig(option *Option) provider.ProviderConfig {
	var cfg provider.ProviderConfig
	if !option.remoteLLM {
		cfg = defaultProviderConfig()
	}
	if len(option.Providers) > 0 {
		cfg = entryToProviderConfig(option.Providers[activeProviderIndex(option)])
	}

	if option.Provider != "" || option.hasExplicit("Provider") {
		cfg.Provider = provider.NormalizeProvider(option.Provider)
	}
	if option.BaseURL != "" || option.hasExplicit("BaseURL") {
		cfg.BaseURL = option.BaseURL
		if option.Provider == "" {
			cfg.Provider = provider.InferFromBaseURL(option.BaseURL)
		}
	}
	if option.APIKey != "" || option.hasExplicit("APIKey") {
		cfg.APIKey = option.APIKey
	}
	if option.Model != "" || option.hasExplicit("Model") {
		cfg.Model = option.Model
	}
	if option.LLMProxy != "" || option.hasExplicit("LLMProxy") {
		cfg.Proxy = option.LLMProxy
	}
	applyProviderLimits(&cfg, option)
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120
	}
	return cfg
}

func FallbackProviderConfigs(option *Option) []provider.ProviderConfig {
	if (!HasSingleProviderFields(option) || option.ActiveProfile != "") && len(option.Providers) > 0 {
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

// Resolve the profile before environment fallback and per-field overrides.
func seedProviderProfile(option, explicit *Option) error {
	seen := map[string]bool{}
	for i := range option.Providers {
		p := &option.Providers[i]
		if p.ID == "" {
			p.ID = fmt.Sprintf("profile-%d", i+1)
		}
		if seen[p.ID] {
			return fmt.Errorf("llm.providers: duplicate id %q", p.ID)
		}
		seen[p.ID] = true
		if p.MaxTokens < 0 || p.ContextWindow < 0 || p.Timeout < 0 {
			return fmt.Errorf("llm.providers.%s: limits must be nonnegative", p.ID)
		}
	}
	if option.ActiveProfile != "" && !seen[option.ActiveProfile] {
		return fmt.Errorf("unknown LLM profile %q", option.ActiveProfile)
	}
	if len(option.Providers) == 0 {
		return nil
	}
	index := activeProviderIndex(option)
	p := option.Providers[index]
	if p.Provider == "" {
		p.Provider = entryToProviderConfig(p).Provider
	}
	option.ActiveProfile = p.ID
	values := map[string]any{"Provider": p.Provider, "BaseURL": p.BaseURL, "APIKey": p.APIKey, "Model": p.Model, "LLMProxy": p.Proxy, "MaxTokens": p.MaxTokens, "ContextWindow": p.ContextWindow}
	for name, value := range values {
		field := reflect.ValueOf(option).Elem().FieldByName(name)
		schema, _ := reflect.TypeOf(option.LLMOptions).FieldByName(name)
		path := "llm." + schema.Tag.Get("config")
		profilePath := "llm.providers." + p.ID + "." + schema.Tag.Get("config")
		profileWins := false
		if option.Snapshot != nil {
			rank := func(source string) int {
				for i, layer := range option.Snapshot.Layers {
					if layer.Path == source {
						return i
					}
				}
				return -1
			}
			profileWins = rank(option.Snapshot.Sources[profilePath]) > rank(option.Snapshot.Sources[path])
		}
		if !explicit.hasExplicit(name) && (field.IsZero() || profileWins) {
			field.Set(reflect.ValueOf(value))
			if option.Snapshot != nil {
				option.Snapshot.Sources[path] = option.Snapshot.Sources[profilePath]
			}
		}
	}
	return nil
}
