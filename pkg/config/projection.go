package config

import (
	types "github.com/chainreactors/cyber/core/types"
)

// LLMFromOption applies resolved field overrides to the active profile in place.
func LLMFromOption(option *Option) *types.LLMConfig {
	llm := &types.LLMConfig{}
	flat := HasSingleProviderFields(option) && len(option.Providers) == 0
	if flat {
		active := ProviderConfig(option)
		llm.Providers = append(llm.Providers, &types.LLMProviderConfig{
			Provider: active.Provider, BaseUrl: active.BaseURL, ApiKey: active.APIKey,
			Model: active.Model, Proxy: active.Proxy, Timeout: int32(active.Timeout),
			MaxTokens: int32(active.MaxTokens), ContextWindow: int32(active.ContextWindow),
		})
	} else {
		llm.ActiveProfile = option.ActiveProfile
	}
	for _, entry := range option.Providers {
		timeout := entry.Timeout
		if timeout <= 0 {
			timeout = 120
		}
		llm.Providers = append(llm.Providers, &types.LLMProviderConfig{
			Id: entry.ID, Name: entry.Name, Provider: entry.Provider, BaseUrl: entry.BaseURL,
			ApiKey: entry.APIKey, Model: entry.Model, Proxy: entry.Proxy, Timeout: int32(timeout),
			Images: entry.Images, MaxTokens: int32(entry.MaxTokens), ContextWindow: int32(entry.ContextWindow),
		})
	}
	if len(option.Providers) > 0 {
		active := ProviderConfig(option)
		index := activeProviderIndex(option)
		target := llm.Providers[index]
		llm.ActiveProfile = target.Id
		target.Provider = active.Provider
		target.BaseUrl = active.BaseURL
		target.ApiKey = active.APIKey
		target.Model = active.Model
		target.Proxy = active.Proxy
		target.MaxTokens = int32(active.MaxTokens)
		target.ContextWindow = int32(active.ContextWindow)
	}
	return llm
}
