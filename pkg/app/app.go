package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/edition"
	"github.com/chainreactors/aiscan/pkg/toolset"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
)

type App struct {
	config            Config
	provider          agent.Provider
	providerConfig    agent.ProviderConfig
	ProviderFallbacks []agent.ProviderEntry
	Commands          *commands.Registry
	Tools             tool.Executor
	Bash              *commands.BashTool
	Hooks             *hooks.Registry
	Skills            *skills.Store
	SkillDiagnostics  []skills.Diagnostic
	events            *coreevents.Stream
	Progress          *eventbus.Bus[*toolpb.Progress]
	scanner           Scanner
	closed            bool
	stateMu           sync.Mutex
	lifecycle         sync.Mutex
	loaded            bool
	closing           bool
	providerMu        sync.RWMutex
	providerRevision  uint64
	llmHealth         LLMHealth
	loggerMu          sync.RWMutex
	logger            telemetry.Logger
}

// Resource owns App initialization and shutdown. Profiles retain Resource and
// publish App, whose API contains no lifecycle operations.
type Resource struct {
	App *App
}

var _ extension.Extension = (*Resource)(nil)

// LLMHealth is the latest lightweight provider connectivity check. It is kept
// separately from ProviderConfig: a syntactically valid configuration can still
// be unreachable or rejected by the remote service.
type LLMHealth struct {
	State     string
	LatencyMs int64
	Error     string
	CheckedAt time.Time
}

const (
	LLMHealthNotConfigured = "not_configured"
	LLMHealthConfigured    = "configured"
	LLMHealthReady         = "ready"
	LLMHealthFailed        = "failed"
)

// Dependencies are profile-selected business capabilities. App uses them but
// never loads or closes their owning resources.
type Dependencies struct {
	Hooks    *hooks.Registry
	Events   *coreevents.Stream
	Commands *commands.Registry
	Tools    *toolset.Registry
	Bash     *commands.BashTool
	Scanner  Scanner
}

// Scanner is the read-only readiness side of the profile-owned scanner
// extension. App reports it to callers but never starts or closes it.
type Scanner interface {
	Wait(context.Context) error
	State() string
}

// New constructs an inert application around extensions selected by its profile.
func New(rc Config, dependencies Dependencies) *Resource {
	if rc.Capabilities.Empty() {
		rc.Capabilities = edition.Catalog()
	}
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	events := dependencies.Events
	if events == nil {
		events = coreevents.New()
	}
	registry := dependencies.Hooks
	if registry == nil {
		registry = hooks.New()
	}
	commandRegistry := dependencies.Commands
	if commandRegistry == nil {
		commandRegistry = commands.NewRegistry(registry)
	}
	toolRegistry := dependencies.Tools
	if toolRegistry == nil {
		toolRegistry = toolset.NewRegistry(registry)
	}
	a := &App{
		config: rc, logger: logger,
		Hooks: registry, events: events,
		Progress: eventbus.New[*toolpb.Progress](), Commands: commandRegistry,
		Tools: toolRegistry, Bash: dependencies.Bash, scanner: dependencies.Scanner,
	}
	return &Resource{App: a}
}

// Load initializes application state. Capability resources and registries are
// sibling entries in the owning profile's Set; App never creates a nested Set.
func (r *Resource) Load(scope *extension.Scope) error {
	if r == nil || r.App == nil || scope == nil {
		return fmt.Errorf("application is required")
	}
	a := r.App
	ctx := scope.Init()
	if ctx == nil {
		ctx = context.Background()
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if a.loaded {
		return nil
	}
	if a.closing {
		return fmt.Errorf("application is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger := a.Logger()
	rc := a.config
	store, diagnostics := skills.LoadAll(rc.CLISkillPaths, rc.Capabilities)
	a.Skills = store
	a.SkillDiagnostics = diagnostics

	if rc.Provider.Enabled {
		// Retain the requested configuration even when provider construction or
		// probing fails, so /status can explain what is configured instead of
		// collapsing every failure into an unhelpful "not configured" state.
		a.providerConfig = rc.Provider.Config
		llmProvider, resolved, err := initProvider(rc.Provider.Config, logger)
		if err != nil {
			a.setLLMHealth(LLMHealth{State: LLMHealthNotConfigured, Error: err.Error(), CheckedAt: time.Now()})
			if !rc.Provider.Optional {
				return err
			}
			logger.Debugf("provider not configured: %s", err)
		} else {
			a.provider = llmProvider
			a.providerConfig = *resolved
			a.setLLMHealth(logLLMProbeStatus(ctx, *resolved, logger))
		}
		for _, fbCfg := range rc.Provider.Fallbacks {
			fbProvider, fbResolved, err := initProvider(fbCfg, logger)
			if err != nil {
				logger.Warnf("fallback provider %s init failed: %s", fbCfg.Provider, err)
				continue
			}
			a.ProviderFallbacks = append(a.ProviderFallbacks, agent.ProviderEntry{
				Provider: fbProvider,
				Model:    fbResolved.Model,
			})
			logger.Infof("fallback provider init provider=%s model=%s", fbResolved.Provider, fbResolved.Model)
		}
	}
	if !rc.Provider.Enabled {
		a.setLLMHealth(LLMHealth{State: LLMHealthNotConfigured})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.loaded = true
	return nil
}

func (a *App) Logger() telemetry.Logger {
	return appLogger{app: a}
}

func (a *App) SetLogger(logger telemetry.Logger) {
	if a == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if proxy, ok := logger.(appLogger); ok && proxy.app == a {
		return
	}
	a.loggerMu.Lock()
	a.logger = logger
	a.loggerMu.Unlock()
}

func (a *App) currentLogger() telemetry.Logger {
	if a == nil {
		return telemetry.NopLogger()
	}
	a.loggerMu.RLock()
	logger := a.logger
	a.loggerMu.RUnlock()
	if logger == nil {
		return telemetry.NopLogger()
	}
	return logger
}

func (a *App) setLLMHealth(health LLMHealth) {
	if a == nil {
		return
	}
	a.providerMu.Lock()
	a.llmHealth = health
	a.providerMu.Unlock()
}

func (a *App) LLMHealth() LLMHealth {
	if a == nil {
		return LLMHealth{State: LLMHealthNotConfigured}
	}
	a.providerMu.RLock()
	health := a.llmHealth
	a.providerMu.RUnlock()
	if health.State == "" {
		health.State = LLMHealthNotConfigured
	}
	return health
}

type appLogger struct {
	app *App
}

func (l appLogger) Debugf(format string, args ...any) { l.app.currentLogger().Debugf(format, args...) }
func (l appLogger) Infof(format string, args ...any)  { l.app.currentLogger().Infof(format, args...) }
func (l appLogger) Warnf(format string, args ...any)  { l.app.currentLogger().Warnf(format, args...) }
func (l appLogger) Errorf(format string, args ...any) { l.app.currentLogger().Errorf(format, args...) }
func (l appLogger) Importantf(format string, args ...any) {
	l.app.currentLogger().Importantf(format, args...)
}

func (a *App) WaitEngines(ctx context.Context) error {
	if a == nil || a.scanner == nil {
		return nil
	}
	return a.scanner.Wait(ctx)
}

func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.App == nil {
		return nil
	}
	a := r.App
	a.lifecycle.Lock()
	a.closing = true
	a.loaded = false
	a.lifecycle.Unlock()
	a.stateMu.Lock()
	a.closed = true
	a.stateMu.Unlock()
	return nil
}

func (a *App) Closed() bool {
	if a == nil {
		return true
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.closed
}

func initProvider(provCfg agent.ProviderConfig, logger telemetry.Logger) (agent.Provider, *agent.ProviderConfig, error) {
	resolved, err := agent.ResolveProvider(&provCfg)
	if err != nil {
		return nil, nil, err
	}
	logger.Infof("provider init provider=%s model=%s", resolved.Provider, resolved.Model)
	llmProvider, err := agent.NewProviderFromResolved(resolved)
	if err != nil {
		return nil, nil, err
	}
	return llmProvider, resolved, nil
}

const startupLLMProbeTimeout = 5 * time.Second

func logLLMProbeStatus(ctx context.Context, provCfg agent.ProviderConfig, logger telemetry.Logger) LLMHealth {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	health := LLMHealth{State: LLMHealthConfigured, CheckedAt: time.Now()}
	probeCtx, cancel := context.WithTimeout(ctx, startupLLMProbeTimeout)
	defer cancel()

	result, err := provider.TestLLM(probeCtx, &types.LLMProbeRequest{
		Provider: provCfg.Provider,
		BaseUrl:  provCfg.BaseURL,
		ApiKey:   provCfg.APIKey,
		Model:    provCfg.Model,
		Proxy:    provCfg.Proxy,
	}, "")
	if err != nil {
		health.State = LLMHealthFailed
		health.Error = err.Error()
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %s", llmConfigLabel(provCfg.Provider, provCfg.Model), err.Error())))
		return health
	}
	health.LatencyMs = result.LatencyMs
	if !result.Ok {
		health.State = LLMHealthFailed
		health.Error = result.Error
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %dms · %s", llmConfigLabel(result.Provider, result.Model), result.LatencyMs, result.Error)))
		return health
	}

	health.State = LLMHealthReady
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
