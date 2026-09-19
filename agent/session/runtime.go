package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	coretool "github.com/chainreactors/cyber/core/tool"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

// ---------------------------------------------------------------------------
// Runtime exposes session operations. Resource owns activation and drain.
// ---------------------------------------------------------------------------

type Runtime struct {
	commands         []Command
	commandIndex     map[string]Command
	history          HistoryStore
	commandMu        sync.RWMutex
	option           *cfg.Option
	logger           telemetry.Logger
	config           Config
	primarySessionID string
	app              *apppkg.App
	hooks            *hooks.Registry
	tools            tool.Executor
	commandRegistry  commands.Executor
	skills           *skills.Store
	bash             *terminaltool.BashTool
	nodeName         string
	promptTarget     prompt.Target
	scannerName      string
	loadedSkills     []prompt.LoadedSkill
	heartbeat        time.Duration
	agentConfig      agent.Config
	resumeMessages   []*aop.Message
	resumeSessionID  string
	ctx              context.Context
	cancel           context.CancelFunc
	providerMu       sync.Mutex
	mu               sync.RWMutex
	sessions         map[string]*sessionState
	runs             map[string]*Run
	requestSeq       uint64
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error
	lifecycle        sync.Mutex
	loaded           bool
	closing          bool
	wg               sync.WaitGroup
	operations       sync.WaitGroup
	maxPending       int
}

type Config struct {
	History     HistoryStore
	BaseSkills  []string
	Commands    []Command
	Application *apppkg.App

	// The runtime borrows these from the host rather than reaching through
	// Application for them: they belong to the extensions that own them, and
	// Application is not a place to park other people's things.
	Hooks            *hooks.Registry
	Tools            tool.Executor
	CommandRegistry  commands.Executor
	Skills           *skills.Store
	Bash             *terminaltool.BashTool
	NodeName         string
	Option           *cfg.Option
	Logger           telemetry.Logger
	PrimarySessionID string
	PromptResolver   prompt.Resolver
	PromptTarget     prompt.Target
	ScannerName      string
	MaxPending       int
	// Loop supplies the algorithm; this extension owns admission and drain.
	Loop agent.Loop
}

// Skills, CommandRegistry, Tools and Bash are what the runtime borrowed from
// the host. They are exposed because the console and the web service present
// the same session; reading them here keeps that view in one place instead of
// parking them on the application for anyone to reach.
func (rt *Runtime) Skills() *skills.Store {
	if rt == nil {
		return nil
	}
	return rt.skills
}

func (rt *Runtime) CommandRegistry() commands.Executor {
	if rt == nil {
		return nil
	}
	return rt.commandRegistry
}

func (rt *Runtime) Hooks() *hooks.Registry {
	if rt == nil {
		return nil
	}
	return rt.hooks
}

func (rt *Runtime) Tools() tool.Executor {
	if rt == nil {
		return nil
	}
	return rt.tools
}

func (rt *Runtime) Bash() *terminaltool.BashTool {
	if rt == nil {
		return nil
	}
	return rt.bash
}

// NodeName is the profile-selected name shared by sessions and node transports.
func (rt *Runtime) NodeName() string {
	if rt == nil {
		return ""
	}
	rt.lifecycle.Lock()
	defer rt.lifecycle.Unlock()
	return rt.nodeName
}

// Start activates session work under the owner's lifetime. The initialization
// context cannot extend that lifetime.
func (r *Resource) Start(ctx, lifetime context.Context) error {
	if r == nil || r.runtime == nil {
		return ErrUnavailable
	}
	return r.runtime.start(ctx, lifetime)
}

func (rt *Runtime) start(ctx, lifetime context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt.lifecycle.Lock()
	defer rt.lifecycle.Unlock()
	if rt.loaded {
		return nil
	}
	if rt.closing {
		return fmt.Errorf("agent runtime is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	application, option := rt.app, rt.option
	logger, rc := rt.logger, rt.config
	runtimeCtx, runtimeCancel := context.WithCancel(lifetime)
	rt.ctx, rt.cancel = runtimeCtx, runtimeCancel
	rt.primarySessionID = rc.PrimarySessionID
	rt.maxPending = rc.MaxPending
	if rt.primarySessionID == "" {
		rt.primarySessionID = "task"
	}
	rt.heartbeat = time.Duration(option.Heartbeat) * time.Minute
	rt.app = application
	provider, providerConfig := rt.app.ProviderState()
	rt.app.SetLogger(logger)
	logger = rt.app.Logger()
	var resumeCounter int64
	if option.Resume != "" {
		data, err := rt.history.Load(ctx, option.Resume)
		if err != nil {
			return fmt.Errorf("resume session: %w", err)
		}
		rt.resumeMessages = data.Messages
		rt.resumeSessionID = data.SessionID
		resumeCounter = data.MessageCounter
		logger.Importantf("resumed %d messages from %s", len(data.Messages), option.Resume)
	}

	nodeName := rc.NodeName
	if nodeName == "" {
		nodeName = "cyber"
	}
	rt.nodeName = nodeName
	executor := coretool.EmptyExecutor()
	if rt.tools != nil {
		executor = rt.tools
	}

	store := rt.skills
	if store == nil {
		store = skills.NewStore(nil)
	}
	rt.promptTarget = rc.PromptTarget
	if rt.promptTarget == "" {
		rt.promptTarget = prompt.MainSystem
	}
	rt.scannerName = rc.ScannerName
	skillNames := option.Skills
	if rt.promptTarget != prompt.ScannerSystem {
		skillNames = append(append([]string(nil), rc.BaseSkills...), skillNames...)
	}
	for _, name := range skillNames {
		if promptHasLoadedSkill(rt.loadedSkills, name) {
			continue
		}
		body := store.ReadBody(name)
		if body == "" {
			body = skills.ReadFile("skills/" + name + ".md")
		}
		if body == "" {
			body = skills.ReadFile(name)
		}
		if body != "" {
			rt.loadedSkills = append(rt.loadedSkills, prompt.LoadedSkill{Name: name, Body: body})
		}
	}

	rt.agentConfig = agent.Config{
		Loop:                  rc.Loop,
		Provider:              provider,
		Tools:                 executor,
		Model:                 providerConfig.Model,
		MaxTokens:             providerConfig.MaxTokens,
		ContextWindow:         providerConfig.ContextWindow,
		Logger:                logger,
		CacheRetention:        agent.CacheShort,
		Bus:                   rt.app,
		Hooks:                 rt.hooks,
		CaptureProviderFrames: option.CaptureProviderFrames,
		MessageCounter:        resumeCounter,
		SystemPromptFn:        rt.resolveSystemPrompt,
		PromptResolver:        rc.PromptResolver,
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	rt.loaded = true
	return nil
}

// ready rejects business admission until the owning profile has completed
// Load. Lifecycle wiring such as namespace binding and Observe may happen
// earlier, but their handlers cannot create sessions or runs through this
// gate.
func (rt *Runtime) ready() error {
	if rt == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	rt.lifecycle.Lock()
	defer rt.lifecycle.Unlock()
	if !rt.loaded || rt.closing {
		return fmt.Errorf("agent runtime is not active")
	}
	return nil
}

func promptHasLoadedSkill(values []prompt.LoadedSkill, name string) bool {
	for _, loaded := range values {
		if loaded.Name == name {
			return true
		}
	}
	return false
}

func (rt *Runtime) resolveSystemPrompt(ctx context.Context, config *agent.Config) (string, error) {
	resolver := rt.agentConfig.PromptResolver
	if config != nil && config.PromptResolver != nil {
		resolver = config.PromptResolver
	}
	if resolver == nil {
		return "", nil
	}
	hostname, _ := os.Hostname()
	input := prompt.Context{Target: rt.promptTarget, Agent: prompt.AgentContext{
		NodeName: rt.nodeName, ScannerName: rt.scannerName,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: hostname,
		Now: time.Now(), Windows: runtime.GOOS == "windows",
		LoadedSkills: append([]prompt.LoadedSkill(nil), rt.loadedSkills...),
	}}
	if config != nil {
		input.Agent.Name, input.Agent.Model = config.AgentName, config.Model
		if config.Tools != nil {
			for _, definition := range config.Tools.ToolDefinitions() {
				input.Agent.Tools = append(input.Agent.Tools, prompt.Tool{Name: definition.Name, Description: definition.Description})
			}
		}
	}
	if rt.commandRegistry != nil {
		input.Agent.ScannerDocs = rt.commandRegistry.UsageDocs()
	}
	if rt.skills != nil {
		for _, value := range rt.skills.All() {
			if !value.Internal {
				input.Agent.Skills = append(input.Agent.Skills, prompt.Skill{Name: value.Name, Description: value.Description, Location: value.Location})
			}
		}
	}
	result := resolver.Build(ctx, input)
	for _, diagnostic := range result.Diagnostics {
		rt.logger.Warnf("prompt contribution=%q section=%q: %s", diagnostic.Contribution, diagnostic.Section, diagnostic.Message)
	}
	return result.Prompt, nil
}

func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.close(ctx)
}

func (rt *Runtime) close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	rt.lifecycle.Lock()
	if rt.closeDone == nil {
		rt.closeDone = make(chan struct{})
	}
	done := rt.closeDone
	rt.lifecycle.Unlock()
	rt.closeOnce.Do(func() {
		rt.lifecycle.Lock()
		rt.closing = true
		rt.lifecycle.Unlock()
		if rt.cancel != nil {
			rt.cancel()
		}
		go func() {
			defer close(rt.closeDone)
			rt.mu.RLock()
			sessions := make([]*sessionState, 0, len(rt.sessions))
			for _, session := range rt.sessions {
				sessions = append(sessions, session)
			}
			rt.mu.RUnlock()
			// OpenSession may derive from an external context. Cancel every session
			// before waiting for any one of them to acknowledge shutdown.
			for _, session := range sessions {
				session.cancel()
			}
			for _, session := range sessions {
				rt.closeErr = errors.Join(rt.closeErr, rt.CloseSession(context.Background(), session.logicalID, SessionCloseRuntime))
			}
			rt.wg.Wait()
			rt.operations.Wait()
			rt.lifecycle.Lock()
			rt.loaded = false
			rt.lifecycle.Unlock()
		}()
	})
	select {
	case <-done:
		return rt.closeErr
	default:
	}
	select {
	case <-done:
		return rt.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rt *Runtime) SetLogger(logger telemetry.Logger) {
	if rt == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	rt.app.SetLogger(logger)
	logger = rt.app.Logger()
	rt.mu.Lock()
	rt.agentConfig.Logger = logger
	for _, sess := range rt.sessions {
		sess.agent.SetLogger(logger)
	}
	rt.mu.Unlock()
}

// ReloadProvider rebuilds application configuration and updates this Runtime's
// template and existing sessions. In-flight runs retain their snapshot.
func (rt *Runtime) ReloadProvider(option *cfg.Option) (agent.Provider, string, error) {
	if option == nil {
		return nil, "", fmt.Errorf("provider option is required")
	}
	provider, resolved, err := rt.reloadProvider(apppkg.ProviderConfig(option))
	return provider, resolved.Model, err
}

func (rt *Runtime) reloadProvider(config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) {
	if rt == nil || rt.app == nil {
		return nil, agent.ProviderConfig{}, fmt.Errorf("agent runtime is not configured")
	}
	rt.providerMu.Lock()
	defer rt.providerMu.Unlock()
	provider, resolved, err := rt.app.ReloadProvider(rt.ctx, config)
	if err != nil {
		return nil, agent.ProviderConfig{}, err
	}
	rt.applyProvider(provider, resolved)
	return provider, resolved, nil
}

func (rt *Runtime) ReloadResolvedProvider(config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) {
	return rt.reloadProvider(config)
}

// SetProvider atomically updates the runtime template and every existing
// conversation session. Runs already in flight keep their provider snapshot.
func (rt *Runtime) SetProvider(provider agent.Provider, providerConfig agent.ProviderConfig) {
	if rt == nil {
		return
	}
	rt.providerMu.Lock()
	defer rt.providerMu.Unlock()
	if rt.app != nil {
		rt.app.SetProvider(provider, providerConfig)
	}
	rt.applyProvider(provider, providerConfig)
}

func (rt *Runtime) applyProvider(provider agent.Provider, providerConfig agent.ProviderConfig) {
	rt.mu.Lock()
	rt.agentConfig.Provider = provider
	if providerConfig.Model != "" {
		rt.agentConfig.Model = providerConfig.Model
	}
	rt.agentConfig.MaxTokens = providerConfig.MaxTokens
	rt.agentConfig.ContextWindow = providerConfig.ContextWindow
	for _, sess := range rt.sessions {
		sess.agent.SetProviderConfig(provider, providerConfig)
	}
	rt.mu.Unlock()
}

// Configured reports whether this runtime has an application behind it. It is
// the readiness a console entry point checks before binding a terminal to it.
func (rt *Runtime) Configured() bool { return rt != nil && rt.app != nil }

// ProviderState is the model this runtime reasons with, and its configuration.
func (rt *Runtime) ProviderState() (agent.Provider, agent.ProviderConfig) {
	if rt == nil || rt.app == nil {
		return nil, agent.ProviderConfig{}
	}
	return rt.app.ProviderState()
}

// ProviderFallbacks are the configured alternatives to the active model.
func (rt *Runtime) ProviderFallbacks() []provider.Entry {
	if rt == nil || rt.app == nil {
		return nil
	}
	return rt.app.Providers.Fallbacks()
}

// Context ends when the runtime shuts down. It is nil before Load.
func (rt *Runtime) Context() context.Context { return rt.ctx }
