package app

import (
	"context"
	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
)

type LLMHealth = provider.Health

const (
	LLMHealthNotConfigured = provider.HealthNotConfigured
	LLMHealthConfigured    = provider.HealthConfigured
	LLMHealthReady         = provider.HealthReady
	LLMHealthFailed        = provider.HealthFailed
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
func (a *App) LLMHealth() LLMHealth {
	if a == nil {
		return LLMHealth{State: LLMHealthNotConfigured}
	}
	return a.Providers.Health()
}
