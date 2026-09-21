package config

import (
	"strings"

	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

// DistributeFromOption projects shared harness configuration for transport and UI.
func DistributeFromOption(option *Option) (*types.DistributeConfig, error) {
	if option == nil {
		return &types.DistributeConfig{}, nil
	}
	reconLimit := 0
	if option.ReconLimit != nil {
		reconLimit = *option.ReconLimit
	}
	keys := make([]string, 0, 2)
	for _, raw := range []string{option.TavilyKey, option.SearchConfig.TavilyKeys} {
		if raw = strings.TrimSpace(raw); raw != "" {
			keys = append(keys, raw)
		}
	}
	extensions, err := ValuesToProto(option.Extensions)
	if err != nil {
		return nil, err
	}
	value := &types.DistributeConfig{
		Llm: LLMFromOption(option),
		Cyberhub: &types.CyberhubConfig{
			Url: option.CyberhubURL, Key: option.CyberhubKey,
			Mode: option.CyberhubMode, Proxy: option.Proxy, Mitm: option.Mitm,
		},
		Recon: &types.ReconConfig{
			FofaKey: option.FofaKey, HunterApiKey: option.HunterAPIKey,
			Proxy: option.ReconProxy, Limit: int32(reconLimit),
		},
		Scan:       &types.ScanConfig{Verify: option.ScanConfig.Verify},
		Search:     &types.SearchConfig{TavilyKeys: strings.Join(keys, ",")},
		Agent:      &types.AgentConfig{Tools: append([]string(nil), option.Tools...), Timeout: proto.Int32(int32(option.Timeout)), Heartbeat: int32(option.Heartbeat), EvalCriteria: option.EvalCriteria, EvalModel: option.EvalModel, EvalRounds: option.EvalRounds, CaptureProviderFrames: option.CaptureProviderFrames},
		Traffic:    &types.TrafficConfig{BodyStorage: option.BodyStorage, BodyMaxBytes: option.BodyMaxBytes, BodyRetentionBytes: option.BodyRetentionBytes},
		Node:       &types.NodeConfig{Id: option.NodeID, Name: option.NodeName},
		Extensions: extensions,
	}
	NormalizeLLMConfig(value.Llm)
	return value, nil
}

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
