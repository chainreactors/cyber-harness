package app

import (
	"context"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
)

func (a *App) ProviderState() (agent.Provider, agent.ProviderConfig) {
	if a == nil {
		return nil, agent.ProviderConfig{}
	}
	return a.Providers.Current()
}
func (a *App) SetProvider(p agent.Provider, config agent.ProviderConfig) { a.Providers.Set(p, config) }
func (a *App) ReloadProvider(ctx context.Context, config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) {
	return a.Providers.Reload(ctx, config, a.Logger())
}
func (a *App) ProviderHealth() provider.Health {
	if a == nil {
		return provider.Health{State: provider.HealthNotConfigured}
	}
	return a.Providers.Health()
}
