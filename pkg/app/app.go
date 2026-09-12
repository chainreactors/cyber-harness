package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/hooks"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/edition"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	toolregistry "github.com/chainreactors/aiscan/pkg/toolset/registry"
	"github.com/chainreactors/aiscan/pkg/toolset/terminaltools"
	filetools "github.com/chainreactors/aiscan/pkg/toolset/workspacefiles"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	arsenaltools "github.com/chainreactors/aiscan/tools/arsenal"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
	searchtools "github.com/chainreactors/aiscan/tools/search"
)

type App struct {
	config            Config
	provider          agent.Provider
	providerConfig    agent.ProviderConfig
	ProviderFallbacks []agent.ProviderEntry
	Commands          *commands.Registry
	Tools             tool.Executor
	toolRegistry      *toolregistry.Registry
	Bash              *commands.BashTool
	Hooks             *hooks.Registry
	Engines           any
	Skills            *skills.Store
	SkillDiagnostics  []skills.Diagnostic
	// fileAudit is the trail the file tools and shell executions report into.
	// It belongs to the application rather than any one transport, so a local
	// run and a remote tool node observe the same thing.
	fileAudit        *fileaudit.Audit
	EventBus         *eventbus.Bus[*aop.Event]
	eventMu          sync.Mutex
	eventSeq         map[string]uint64
	Progress         *eventbus.Bus[*toolpb.Progress]
	Recorder         *output.JSONLRecorder
	workDir          string
	toolConfig       ToolConfig
	scannerConfig    ScannerConfig
	resourceSet      *resources.Set
	proxyURL         string
	proxyCA          string
	egressResolver   func(callID string) (proxyURL, caPath string)
	proxyInfra       *proxytool.Infra
	extensions       *extension.Set
	ctx              context.Context
	cancel           context.CancelFunc
	closed           bool
	assemblyMu       sync.Mutex
	recorderMu       sync.Mutex
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error
	lifecycle        sync.Mutex
	loaded           bool
	closing          bool
	enginesReady     chan struct{}
	enginesErr       error
	enginesEnabled   bool
	providerMu       sync.RWMutex
	providerRevision uint64
	llmHealth        LLMHealth
	loggerMu         sync.RWMutex
	logger           telemetry.Logger
}

var _ extension.Extension = (*App)(nil)

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

// New constructs an inert application around concrete modules owned by its
// profile. Provider probing, filesystem discovery, tool publication and engine
// startup begin in Load.
func New(rc Config, fileAudit *fileaudit.Audit, proxyInfra *proxytool.Infra) *App {
	if rc.Capabilities.Empty() {
		rc.Capabilities = edition.Catalog()
	}
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	toolRuntime := toolregistry.New()
	a := &App{
		config: rc, fileAudit: fileAudit, proxyInfra: proxyInfra, logger: logger,
		Hooks: hooks.New(), EventBus: eventbus.New[*aop.Event](),
		Progress: eventbus.New[*toolpb.Progress](), Commands: commands.NewRegistry(),
		Tools: toolRuntime, toolRegistry: toolRuntime, enginesReady: make(chan struct{}), closeDone: make(chan struct{}),
	}
	return a
}

// Load activates the fixed application composition. ctx bounds startup only;
// the application owns its lifetime context until Close.
func (a *App) Load(scope *extension.Context) error {
	ctx := scope.Init()
	if a == nil {
		return fmt.Errorf("application is required")
	}
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
	appCtx, cancel := context.WithCancel(context.Background())
	a.ctx, a.cancel = appCtx, cancel
	enginesDone := false
	defer func() {
		if !enginesDone {
			close(a.enginesReady)
		}
	}()

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
	if err := a.toolRegistry.LoadContext(ctx); err != nil {
		return fmt.Errorf("load tool registry: %w", err)
	}
	if err := a.initCommands(rc, logger); err != nil {
		return err
	}
	if rc.RecordFile != "" {
		if err := a.StartRecording(rc.RecordFile); err != nil {
			return err
		}
	}

	a.enginesEnabled = !rc.SkipEngines
	if a.enginesEnabled {
		a.enginesErr = a.initScanner(ctx, rc, logger)
	}
	close(a.enginesReady)
	enginesDone = true
	if a.enginesErr != nil {
		return a.enginesErr
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
	select {
	case <-a.enginesReady:
		return a.enginesErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.lifecycle.Lock()
	if a.closeDone == nil {
		a.closeDone = make(chan struct{})
	}
	done := a.closeDone
	a.lifecycle.Unlock()
	a.closeOnce.Do(func() {
		a.lifecycle.Lock()
		a.closing = true
		waitEngines := a.loaded
		a.lifecycle.Unlock()
		if a.cancel != nil {
			a.cancel()
		}
		a.assemblyMu.Lock()
		a.closed = true
		a.assemblyMu.Unlock()
		go func() {
			defer close(a.closeDone)
			var closeErr error
			if waitEngines && a.enginesReady != nil {
				<-a.enginesReady
			}
			if a.extensions != nil {
				if err := a.extensions.Close(context.Background()); err != nil {
					closeErr = errors.Join(closeErr, fmt.Errorf("close application extensions: %w", err))
				}
			}
			if a.Commands != nil {
				if err := a.Commands.Close(context.Background()); err != nil {
					closeErr = errors.Join(closeErr, fmt.Errorf("close command registry: %w", err))
				}
			}
			if a.toolRegistry != nil {
				if err := a.toolRegistry.Close(context.Background()); err != nil {
					closeErr = errors.Join(closeErr, fmt.Errorf("close tool registry: %w", err))
					a.Logger().Warnf("close tool registry: %s", err)
				}
			}
			a.recorderMu.Lock()
			if a.Recorder != nil {
				if err := a.Recorder.Close(); err != nil {
					closeErr = errors.Join(closeErr, fmt.Errorf("close AOP JSONL recorder: %w", err))
					a.Logger().Warnf("close AOP JSONL recorder: %s", err)
				}
				a.Recorder = nil
			}
			a.recorderMu.Unlock()
			if closer, ok := a.Engines.(interface{ Close() }); ok {
				closer.Close()
			}
			a.lifecycle.Lock()
			a.loaded = false
			a.closeErr = closeErr
			a.lifecycle.Unlock()
		}()
	})
	select {
	case <-done:
	default:
		select {
		case <-done:
		case <-ctx.Done():
			return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
		}
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	err := a.closeErr
	a.closeErr = nil
	return err
}

func (a *App) StartRecording(path string) error {
	if a == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	a.assemblyMu.Lock()
	defer a.assemblyMu.Unlock()
	if a.closed {
		return fmt.Errorf("application is closed")
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
	a.assemblyMu.Lock()
	defer a.assemblyMu.Unlock()
	if a.closed {
		return fmt.Errorf("application is closed")
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

func providerWebSearch(model agent.Provider) func(context.Context, string, int) (string, error) {
	searcher, ok := model.(provider.WebSearchProvider)
	if !ok {
		return nil
	}
	return func(ctx context.Context, query string, maxResults int) (string, error) {
		response, err := searcher.WebSearch(ctx, query, maxResults)
		if err != nil {
			return "", err
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Web search results for: %s\n\n", query)
		if len(response.Results) == 0 && response.Summary == "" {
			text.WriteString("No results found.\n")
			return text.String(), nil
		}
		for index, result := range response.Results {
			fmt.Fprintf(&text, "[%d] %s\n    URL: %s\n\n", index+1, result.Title, result.URL)
		}
		if response.Summary != "" {
			text.WriteString("Summary:\n")
			text.WriteString(response.Summary)
			text.WriteByte('\n')
		}
		return text.String(), nil
	}
}

func (a *App) initCommands(rc Config, logger telemetry.Logger) error {
	workDir, _ := os.Getwd()
	a.workDir = workDir
	a.toolConfig = rc.Tools
	a.scannerConfig = rc.Scanner
	a.proxyURL = rc.Scanner.Proxy
	var err error
	var extensionEntries []extension.Entry
	if a.proxyInfra != nil && a.proxyInfra.Hub != nil && a.proxyInfra.Hub.ProxyURL() != "" {
		a.proxyURL = a.proxyInfra.Hub.ProxyURL()
		a.proxyCA = a.proxyInfra.Hub.CAPath()
		a.egressResolver = a.proxyInfra.Egress
	}

	plan := rc.Capabilities.Select(capability.Options{
		Groups:        linkedBaseGroups(rc.Capabilities),
		OptionalTools: rc.Tools.OptionalTools,
	})
	if plan.Has("core") {
		workspace, workspaceErr := filetools.NewWorkspace(a.toolRegistry, "workspace", workDir, a.Skills, a.fileAudit, rc.Tools.RunnerMode)
		if workspaceErr != nil {
			return workspaceErr
		}
		terminal, terminalErr := terminaltools.New(a.toolRegistry, a.Commands, terminaltools.Config{
			Directory: workDir, Timeout: rc.Tools.BashTimeout,
			Proxy: a.proxyURL, ProxyCA: a.proxyCA, Egress: a.egressResolver, Audit: a.fileAudit,
		})
		if terminalErr != nil {
			return terminalErr
		}
		a.Bash = terminal.Bash()
		extensionEntries = append(extensionEntries,
			extension.Entry{ID: "workspace", Extension: workspace},
			extension.Entry{ID: "terminal", Extension: terminal},
		)
		subagent := agent.NewSubAgentTool(func(name string) (agent.AgentType, error) {
			if a.Skills == nil {
				return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
			}
			skill, ok := a.Skills.ByName(name)
			if !ok {
				return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
			}
			if !skill.Agent {
				return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
			}
			return agent.AgentType{
				FormattedPrompt: a.Skills.FormatInvocation(skill, ""),
				Model:           skill.AgentModel, Background: skill.AgentBackground,
			}, nil
		})
		if err := a.toolRegistry.Register("subagent", subagent); err != nil {
			return fmt.Errorf("register subagent tool: %w", err)
		}
	}
	if plan.Has("proxy") {
		if err := proxytool.Register(a.Commands, a.proxyInfra, rc.Scanner.Proxy); err != nil {
			return err
		}
	}
	if plan.Has("arsenal") {
		if err := arsenaltools.Register(a.Commands); err != nil {
			logger.Warnf("arsenal init: %v", err)
		}
	}
	if plan.Has("search") {
		if err := searchtools.Register(a.Commands, a.toolRegistry, providerWebSearch(a.provider), rc.Tools.TavilyKeys, a.proxyURL, a.proxyCA, nil, nil); err != nil {
			return err
		}
	}
	entries, err := editionToolEntries(a, rc, plan)
	if err != nil {
		return err
	}
	extensionEntries = append(extensionEntries, entries...)
	if len(extensionEntries) > 0 {
		a.extensions, err = extension.New(extensionEntries...)
		if err != nil {
			return err
		}
		if err := a.extensions.Load(a.ctx); err != nil {
			return err
		}
	}
	return nil
}

func linkedBaseGroups(catalog capability.Catalog) []string {
	seen := make(map[string]bool)
	var groups []string
	for _, descriptor := range catalog.All() {
		baseService := descriptor.Kind == capability.KindService
		if (descriptor.Kind != capability.KindTool && !baseService) || descriptor.Group == "" || seen[descriptor.Group] {
			continue
		}
		seen[descriptor.Group] = true
		groups = append(groups, descriptor.Group)
	}
	return groups
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		left, right = filepath.Clean(leftAbs), filepath.Clean(rightAbs)
	} else {
		left, right = filepath.Clean(left), filepath.Clean(right)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
