package provider

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	"strings"
	"sync"
	"time"
)

type Health struct {
	State     string
	LatencyMs int64
	Error     string
	CheckedAt time.Time
}

const (
	HealthNotConfigured = "not_configured"
	HealthConfigured    = "configured"
	HealthReady         = "ready"
	HealthFailed        = "failed"
)

type Entry struct {
	Provider Provider
	Model    string
}

// StartupMode expresses whether a product omits, requires, or opportunistically
// initializes its provider.
type StartupMode uint8

const (
	StartupDisabled StartupMode = iota
	StartupRequired
	StartupOptional
)

type StartupConfig struct {
	Mode      StartupMode
	Config    ProviderConfig
	Fallbacks []ProviderConfig
}

// State is provider business state. The profile owns its initialization.
type State struct {
	mu        sync.RWMutex
	provider  Provider
	config    ProviderConfig
	revision  uint64
	health    Health
	fallbacks []Entry
	owned     []Provider
}

func (s *State) Current() (Provider, ProviderConfig) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provider, s.config
}
func (s *State) Health() Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.health
	if value.State == "" {
		value.State = HealthNotConfigured
	}
	return value
}
func (s *State) Fallbacks() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.fallbacks...)
}
func (s *State) Set(p Provider, config ProviderConfig) {
	s.install(p, config, Health{State: HealthConfigured, CheckedAt: time.Now()})
}
func (s *State) install(p Provider, config ProviderConfig, health Health) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider, s.config, s.health = p, config, health
	s.revision++
	return s.revision
}
func (s *State) Reload(ctx context.Context, config ProviderConfig, logger telemetry.Logger) (Provider, ProviderConfig, error) {
	p, resolved, err := initProvider(config, logger)
	if err != nil {
		return nil, ProviderConfig{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	s.owned = append(s.owned, p)
	s.mu.Unlock()
	revision := s.install(p, *resolved, Health{State: HealthConfigured, CheckedAt: time.Now()})
	health := Probe(ctx, *resolved, logger)
	s.mu.Lock()
	if s.revision == revision {
		s.health = health
	}
	s.mu.Unlock()
	return p, *resolved, nil
}
func (s *State) initialize(ctx context.Context, config StartupConfig, logger telemetry.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch config.Mode {
	case StartupDisabled:
		return nil
	case StartupRequired, StartupOptional:
	default:
		return fmt.Errorf("invalid provider startup mode %d", config.Mode)
	}
	s.install(nil, config.Config, Health{State: HealthNotConfigured})
	if _, _, err := s.Reload(ctx, config.Config, logger); err != nil {
		s.install(nil, config.Config, Health{State: HealthNotConfigured, Error: err.Error(), CheckedAt: time.Now()})
		if config.Mode != StartupOptional {
			return err
		}
		logger.Debugf("provider not configured: %s", err)
	}
	var fallbacks []Entry
	for _, config := range config.Fallbacks {
		p, resolved, err := initProvider(config, logger)
		if err != nil {
			logger.Warnf("fallback provider %s init failed: %s", config.Provider, err)
			continue
		}
		fallbacks = append(fallbacks, Entry{Provider: p, Model: resolved.Model})
	}
	s.mu.Lock()
	for _, fallback := range fallbacks {
		s.owned = append(s.owned, fallback.Provider)
	}
	s.fallbacks = fallbacks
	s.mu.Unlock()
	return ctx.Err()
}

// reset is called by the owning extension after dependent sessions have drained.
// Providers supplied through Set are borrowed; only locally built clients close.
func (s *State) reset() {
	s.mu.Lock()
	owned := s.owned
	s.owned = nil
	s.provider = nil
	s.config = ProviderConfig{}
	s.health = Health{}
	s.fallbacks = nil
	s.revision++
	s.mu.Unlock()
	for _, p := range owned {
		closeIdleConnections(p)
	}
}
func closeIdleConnections(p Provider) {
	if closer, ok := p.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
func initProvider(provCfg ProviderConfig, logger telemetry.Logger) (Provider, *ProviderConfig, error) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	resolved, err := Resolve(&provCfg)
	if err != nil {
		return nil, nil, err
	}
	logger.Infof("provider init provider=%s model=%s", resolved.Provider, resolved.Model)
	llmProvider, err := NewProviderFromResolved(resolved)
	if err != nil {
		return nil, nil, err
	}
	return llmProvider, resolved, nil
}

const startupLLMProbeTimeout = 5 * time.Second

func Probe(ctx context.Context, provCfg ProviderConfig, logger telemetry.Logger) Health {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	health := Health{State: HealthConfigured, CheckedAt: time.Now()}
	probeCtx, cancel := context.WithTimeout(ctx, startupLLMProbeTimeout)
	defer cancel()

	result, err := TestLLM(probeCtx, &types.LLMProbeRequest{
		Provider: provCfg.Provider,
		BaseUrl:  provCfg.BaseURL,
		ApiKey:   provCfg.APIKey,
		Model:    provCfg.Model,
		Proxy:    provCfg.Proxy,
	}, "")
	if err != nil {
		health.State = HealthFailed
		health.Error = err.Error()
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %s", llmConfigLabel(provCfg.Provider, provCfg.Model), err.Error())))
		return health
	}
	health.LatencyMs = result.LatencyMs
	if !result.Ok {
		health.State = HealthFailed
		health.Error = result.Error
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %dms · %s", llmConfigLabel(result.Provider, result.Model), result.LatencyMs, result.Error)))
		return health
	}

	health.State = HealthReady
	logger.Infof("%s", telemetry.StartupOK("llm", fmt.Sprintf("%s · %dms", llmConfigLabel(result.Provider, result.Model), result.LatencyMs)))
	return health
}

func llmConfigLabel(providerName, model string) string {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		providerName = "unknown"
	}
	if model == "" {
		return providerName
	}
	return providerName + "/" + model
}

// Initialize is the owner's setup boundary. The returned cleanup belongs to the
// caller even on initialization failure; it runs after dependent calls drain.
// State itself exposes only business methods to consumers.
func Initialize(ctx context.Context, state *State, config StartupConfig, logger telemetry.Logger) (func(), error) {
	if state == nil {
		return nil, fmt.Errorf("provider state is required")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return state.reset, state.initialize(ctx, config, logger)
}
