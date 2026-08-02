package web

import (
	"context"

	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

// ConfigStore and PreparedConfig keep the host integration surface stable;
// their implementation and all config business semantics live in web/api.
type ConfigStore = managementapi.ConfigStore
type PreparedConfig = managementapi.PreparedConfig

func (s *Service) GetConfigView(ctx context.Context) (*types.ConfigView, error) {
	return s.api.Config.View(ctx)
}

func (s *Service) SaveConfig(ctx context.Context, config *types.DistributeConfig) (*types.ConfigView, error) {
	return s.api.Config.Save(ctx, config)
}

func (s *Service) ActivateLLMProfile(ctx context.Context, id string) (*types.ConfigView, error) {
	return s.api.Config.Activate(ctx, id)
}

func (s *Service) GetDistributeConfig(ctx context.Context) (*types.DistributeConfig, error) {
	return s.api.Config.Distribute(ctx)
}

func ConfigViewFromDistribute(config *types.DistributeConfig, path string, loaded bool) *types.ConfigView {
	return managementapi.ConfigView(config, path, loaded)
}

func ValidateLLMConfig(config *types.LLMConfig) error {
	return managementapi.ValidateLLMConfig(config)
}
