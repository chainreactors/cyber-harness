package app

import (
	"context"

	"github.com/chainreactors/cyber/agent/provider"
)

func (a *State) ProviderState() (provider.Provider, provider.ProviderConfig) {
	if a == nil {
		return nil, provider.ProviderConfig{}
	}
	return a.Providers.Current()
}
func (a *State) SetProvider(p provider.Provider, config provider.ProviderConfig) {
	a.Providers.Set(p, config)
}
func (a *State) ReloadProvider(ctx context.Context, config provider.ProviderConfig) (provider.Provider, provider.ProviderConfig, error) {
	return a.Providers.Reload(ctx, config, a.Logger())
}
func (a *State) ProviderHealth() provider.Health {
	if a == nil {
		return provider.Health{State: provider.HealthNotConfigured}
	}
	return a.Providers.Health()
}
