package service

import (
	"context"

	"github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
	configpkg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/proto"
)

// nodeConfig snapshots committed settings and the active runtime together.
// Nodes receive effective credentials, including environment and CLI overrides.
func (s *Service) nodeConfig(ctx context.Context) (*types.DistributeConfig, error) {
	select {
	case s.configGate <- struct{}{}:
		defer func() { <-s.configGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	stored, err := s.api.Config.Distribute(ctx)
	if err != nil {
		return nil, err
	}
	return s.configWithRuntimeLLM(stored), nil
}

// configWithRuntimeLLM runs under configGate, after any profile swap. Work on a
// copy so runtime secrets are never written back to the configuration store.
func (s *Service) configWithRuntimeLLM(stored *types.DistributeConfig) *types.DistributeConfig {
	distributed := proto.CloneOf(stored)
	effective := s.runtimeLLMConfig()
	if effective == (provider.ProviderConfig{}) {
		return distributed
	}
	if distributed == nil {
		distributed = &types.DistributeConfig{}
	}
	if distributed.Llm == nil {
		distributed.Llm = &types.LLMConfig{}
	}
	active := configpkg.ActiveLLMProvider(distributed.Llm)
	if active == nil {
		active = &types.LLMProviderConfig{Id: "server-runtime", Name: "Server runtime"}
		distributed.Llm.Providers = append(distributed.Llm.Providers, active)
		distributed.Llm.ActiveProfile = active.Id
	}
	active.Provider = effective.Provider
	active.BaseUrl = effective.BaseURL
	active.ApiKey = effective.APIKey
	active.Model = effective.Model
	active.Proxy = effective.Proxy
	active.Timeout = int32(effective.Timeout)
	active.Images = nil
	if effective.Images != nil {
		active.Images = proto.Bool(*effective.Images)
	}
	active.MaxTokens = int32(effective.MaxTokens)
	active.ContextWindow = int32(effective.ContextWindow)
	return distributed
}

func (s *Service) runtimeLLMConfig() provider.ProviderConfig {
	if providers := s.providers(); providers != nil {
		_, effective := providers.Current()
		return effective
	}
	return provider.ProviderConfig{}
}
