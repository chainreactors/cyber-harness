package app

import (
	"context"
	"time"

	"github.com/chainreactors/aiscan/agent"
)

// ProviderState returns a consistent provider and configuration pair.
func (a *App) ProviderState() (agent.Provider, agent.ProviderConfig) {
	if a == nil {
		return nil, agent.ProviderConfig{}
	}
	a.providerMu.RLock()
	defer a.providerMu.RUnlock()
	return a.provider, a.providerConfig
}

// SetProvider installs an already constructed provider. Runtime owns propagation
// to its session templates; App never retains or calls a Runtime.
func (a *App) SetProvider(provider agent.Provider, config agent.ProviderConfig) {
	a.setProvider(provider, config, LLMHealth{State: LLMHealthConfigured, CheckedAt: time.Now()})
}

func (a *App) setProvider(provider agent.Provider, config agent.ProviderConfig, health LLMHealth) uint64 {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	a.provider, a.providerConfig, a.llmHealth = provider, config, health
	a.providerRevision++
	return a.providerRevision
}

// ReloadProvider leaves the current provider untouched on construction failure.
// A failed connectivity probe records health but does not reject valid config.
func (a *App) ReloadProvider(ctx context.Context, config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) {
	provider, resolved, err := initProvider(config, a.Logger())
	if err != nil {
		return nil, agent.ProviderConfig{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	revision := a.setProvider(provider, *resolved, LLMHealth{State: LLMHealthConfigured, CheckedAt: time.Now()})
	health := logLLMProbeStatus(ctx, *resolved, a.Logger())
	a.providerMu.Lock()
	// Another runtime may have installed a provider while this probe ran.
	if a.providerRevision == revision {
		a.llmHealth = health
	}
	a.providerMu.Unlock()
	return provider, *resolved, nil
}
