package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/hooks"
	"github.com/chainreactors/aiscan/agent/probe"
	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

type App struct {
	Provider          agent.Provider
	ProviderConfig    agent.ProviderConfig
	ProviderFallbacks []agent.ProviderEntry
	Commands          *commands.CommandRegistry
	Hooks             *hooks.Registry
	Engines           any
	Skills            *skills.Store
	SkillDiagnostics  []skills.Diagnostic
	IOAClient         *ioaclient.Client
	IOAStreamClient   ioaclient.StreamAPI
	// FileAudit is the trail the file tools and shell executions report into.
	// It belongs to the application rather than any one transport, so a local
	// run and a remote tool node observe the same thing.
	FileAudit      *commands.FileAudit
	EventBus       *eventbus.Bus[*aop.Event]
	Events         *sessionEmitter
	Progress       *eventbus.Bus[*toolpb.Progress]
	Recorder       *output.JSONLRecorder
	deps           *commands.Deps
	proxyInfra     *proxytool.Infra
	cancel         context.CancelFunc
	assemblyMu     sync.Mutex
	recorderMu     sync.Mutex
	closeOnce      sync.Once
	enginesReady   chan struct{}
	enginesEnabled bool
	healthMu       sync.RWMutex
	llmHealth      LLMHealth
	loggerMu       sync.RWMutex
	logger         telemetry.Logger
}

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

func NewApp(ctx context.Context, rc ApplicationConfig) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	appCtx, cancel := context.WithCancel(ctx)
	a := &App{cancel: cancel}
	ready := false
	defer func() {
		if !ready {
			cancel()
		}
	}()
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a.logger = logger
	logger = a.Logger()
	a.Hooks = hooks.New()
	a.Hooks.SetErrorSink(func(he *hooks.HandlerError) {
		if len(he.Stack) > 0 {
			a.Logger().Errorf("hook panic kind=%s source=%s panic=%v\n%s", he.Kind, he.Source, he.Panic, he.Stack)
			return
		}
		a.Logger().Warnf("hook failed kind=%s source=%s error=%q", he.Kind, he.Source, he.Err)
	})

	a.EventBus = eventbus.New[*aop.Event]()
	a.Events = newSessionEmitter(a.EventBus)
	a.Progress = eventbus.New[*toolpb.Progress]()

	store, diagnostics := skills.LoadAll(rc.CLISkillPaths)
	a.Skills = store
	a.SkillDiagnostics = diagnostics

	if rc.Provider.Enabled {
		// Retain the requested configuration even when provider construction or
		// probing fails, so /status can explain what is configured instead of
		// collapsing every failure into an unhelpful "not configured" state.
		a.ProviderConfig = rc.Provider.Config
		llmProvider, resolved, err := initProvider(rc.Provider.Config, logger)
		if err != nil {
			a.setLLMHealth(LLMHealth{State: LLMHealthNotConfigured, Error: err.Error(), CheckedAt: time.Now()})
			if !rc.Provider.Optional {
				return nil, err
			}
			logger.Debugf("provider not configured: %s", err)
		} else {
			a.Provider = llmProvider
			a.ProviderConfig = *resolved
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

	a.FileAudit = commands.NewFileAudit()
	a.initCommands(rc, logger)
	if rc.RecordFile != "" {
		if err := a.StartRecording(rc.RecordFile); err != nil {
			a.Close()
			return nil, err
		}
	}

	a.enginesReady = make(chan struct{})
	a.enginesEnabled = !rc.SkipEngines
	go func() {
		if a.enginesEnabled {
			a.initScanner(appCtx, rc, logger)
		}
		close(a.enginesReady)
	}()

	if rc.IOA != nil {
		if err := a.InitIOA(appCtx, *rc.IOA); err != nil {
			a.Close()
			return nil, err
		}
	}

	ready = true
	return a, nil
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
	if a.Commands != nil {
		a.Commands.SetLogger(a.Logger())
	}
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
	a.healthMu.Lock()
	a.llmHealth = health
	a.healthMu.Unlock()
}

func (a *App) LLMHealth() LLMHealth {
	if a == nil {
		return LLMHealth{State: LLMHealthNotConfigured}
	}
	a.healthMu.RLock()
	health := a.llmHealth
	a.healthMu.RUnlock()
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
	select {
	case <-a.enginesReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) Close() {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		if a.enginesReady != nil {
			<-a.enginesReady
		}
		a.recorderMu.Lock()
		if a.Recorder != nil {
			if err := a.Recorder.Close(); err != nil {
				a.Logger().Warnf("close AOP JSONL recorder: %s", err)
			}
			a.Recorder = nil
		}
		a.recorderMu.Unlock()
		if a.Commands != nil {
			for _, t := range a.Commands.Tools() {
				if closer, ok := t.(interface{ Close() }); ok {
					closer.Close()
				}
			}
			for _, cmd := range a.Commands.All() {
				if cmd.Close != nil {
					cmd.Close()
				}
			}
		}
		if closer, ok := a.Engines.(interface{ Close() }); ok {
			closer.Close()
		}
		if a.proxyInfra != nil && a.proxyInfra.Hub != nil {
			a.proxyInfra.Hub.Shutdown(nil)
		}
		if a.FileAudit != nil {
			a.FileAudit.Close()
		}
	})
}

func (a *App) StartRecording(path string) error {
	if a == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	a.recorderMu.Lock()
	defer a.recorderMu.Unlock()
	if a.Recorder != nil {
		if !samePath(a.Recorder.Path(), path) {
			return fmt.Errorf("AOP JSONL already records to %s", a.Recorder.Path())
		}
		return nil
	}
	recorder, err := output.NewJSONLRecorder(a.EventBus, path)
	if err != nil {
		return err
	}
	a.Recorder = recorder
	return nil
}

func (a *App) SwitchRecording(path string) error {
	if a == nil || strings.TrimSpace(path) == "" {
		return fmt.Errorf("AOP JSONL path is required")
	}
	a.recorderMu.Lock()
	defer a.recorderMu.Unlock()
	if a.Recorder == nil {
		recorder, err := output.NewJSONLRecorder(a.EventBus, path)
		if err != nil {
			return err
		}
		a.Recorder = recorder
		return nil
	}
	if samePath(a.Recorder.Path(), path) {
		return nil
	}
	return a.Recorder.Switch(path)
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

	result, err := probe.TestLLM(probeCtx, &types.LLMProbeRequest{
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

func (a *App) initCommands(rc ApplicationConfig, logger telemetry.Logger) {
	a.Commands = commands.NewRegistry()
	workDir, _ := os.Getwd()
	a.deps = &commands.Deps{
		WorkDir:           workDir,
		RunnerMode:        rc.Tools.RunnerMode,
		BashTimeout:       rc.Tools.BashTimeout,
		SkillStore:        a.Skills,
		Provider:          a.Provider,
		ScannerProxy:      rc.Scanner.Proxy,
		Logger:            logger,
		TavilyKeys:        rc.Tools.TavilyKeys,
		PlaywrightSession: rc.Tools.PlaywrightSession,
		Hooks:             a.Hooks,
		Events:            a.Events,
		FileAudit:         a.FileAudit,
	}
	var err error
	a.proxyInfra, err = proxytool.InstallInfra(a.deps, captureEnabled(rc.Tools.MitmCapture))
	if err != nil {
		logger.Warnf("proxy hub unavailable, tools use direct/original proxy: %s", err)
	}

	plan := capability.Select(capability.Options{
		Groups:        linkedBaseGroups(),
		OptionalTools: rc.Tools.OptionalTools,
	})
	commands.BuildPlan(plan, a.deps, a.Commands)
	a.Commands.SetLogger(logger)
}

func linkedBaseGroups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, descriptor := range capability.All() {
		baseService := descriptor.Kind == capability.KindService && len(descriptor.Requires) == 0
		if (descriptor.Kind != capability.KindTool && !baseService) || descriptor.Group == "" || seen[descriptor.Group] {
			continue
		}
		seen[descriptor.Group] = true
		groups = append(groups, descriptor.Group)
	}
	return groups
}

func captureEnabled(configured *bool) bool {
	return configured == nil || *configured
}

// RegisterTrafficNamespace exposes the application's single proxy hub over AOP.
func (a *App) RegisterTrafficNamespace(mux *aop.NamespaceMux) error {
	if a == nil || a.proxyInfra == nil || a.proxyInfra.Hub == nil {
		return nil
	}
	return proxytool.NewTrafficHandler(a.proxyInfra).Register(mux)
}

func (a *App) InitIOA(ctx context.Context, ioa IOAConfig) error {
	client, err := newIOAClient(ioa)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	a.IOAClient = client
	if ioa.Identity != nil {
		if err := client.Bind(ioa.Identity); err != nil {
			return fmt.Errorf("bind ioa identity: %w", err)
		}
	}
	a.IOAStreamClient = client
	if ioa.RegisterTools && a.Commands != nil && a.deps != nil {
		a.assemblyMu.Lock()
		a.deps.NodeName = ioa.NodeName
		a.deps.NodeMeta = ioa.NodeMeta
		commands.Provide(a.deps, ioatools.ClientKey, protocols.ClientAPI(client))
		commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"ioa"}}), a.deps, a.Commands)
		a.assemblyMu.Unlock()
	}
	if ioa.AutoRegister {
		if err := client.EnsureRegistered(ctx, ioa.NodeName, "", ioa.NodeMeta); err != nil {
			a.Logger().Warnf("ioa registration pending: %s", err)
			telemetry.SafeGo("ioa-registration-retry", func() {
				a.retryIOARegistration(ctx, client, ioa)
			})
			return nil
		}
	}
	a.configureIOASpace(ctx, client, ioa)
	return nil
}

func (a *App) retryIOARegistration(ctx context.Context, client *ioaclient.Client, ioa IOAConfig) {
	for attempt := 0; ; attempt++ {
		delay := agent.RetryDelay(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if client.EnsureRegistered(ctx, ioa.NodeName, "", ioa.NodeMeta) == nil {
			a.Logger().Infof("ioa node registered: %s", client.NodeID())
			a.configureIOASpace(ctx, client, ioa)
			return
		}
	}
}

func (a *App) configureIOASpace(ctx context.Context, client *ioaclient.Client, ioa IOAConfig) {
	if ioa.Space != "" && client != nil && client.Bound() {
		info, err := client.Space(ctx, ioa.Space, "aiscan agent")
		if err == nil {
			a.setIOASpace(info.ID)
		}
	}
}

func (a *App) setIOASpace(spaceID string) {
	for _, cmd := range a.Commands.All() {
		if cmd.SetDefaultSpace != nil {
			cmd.SetDefaultSpace(spaceID)
		}
	}
}

func newIOAClient(ioa IOAConfig) (*ioaclient.Client, error) {
	if ioa.URL == "" {
		return nil, nil
	}
	return ioaclient.NewClient(ioa.URL, ioa.NodeID)
}
