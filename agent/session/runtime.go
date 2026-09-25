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
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
)

// ---------------------------------------------------------------------------
// Runtime exposes session operations. Resource owns activation and drain.
// ---------------------------------------------------------------------------

type Runtime struct {
	// Logger is borrowed from the installation; SetOutput keeps existing consumers attached.
	Logger           telemetry.Logger
	commands         []Command
	commandIndex     map[string]Command
	history          HistoryStore
	config           Config
	primarySessionID string
	providers        *provider.State
	events           *events.Stream
	hooks            *hooks.Registry
	tools            coretool.Executor
	commandRegistry  coretool.CommandExecutor
	skills           *skills.Store
	shell            coretool.Tool
	nodeName         string
	promptTarget     prompt.Target
	commandName      string
	loadedSkills     []prompt.LoadedSkill
	heartbeat        time.Duration
	agentConfig      agent.Config
	resumeMessages   []*aop.Message
	resumeSessionID  string
	ctx              context.Context
	cancel           context.CancelFunc
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
	History    HistoryStore
	BaseSkills []string
	Commands   []Command
	Providers  *provider.State
	Events     *events.Stream

	// Capabilities are borrowed from their owning extensions.
	Hooks                 *hooks.Registry
	Tools                 coretool.Executor
	CommandRegistry       coretool.CommandExecutor
	Skills                *skills.Store
	Shell                 coretool.Tool
	NodeName              string
	Heartbeat             time.Duration
	Resume                string
	SelectedSkills        []string
	CaptureProviderFrames bool
	Logger                telemetry.Logger
	PrimarySessionID      string
	PromptResolver        prompt.Resolver
	PromptTarget          prompt.Target
	// CommandName identifies the command-focused worker session (e.g. a
	// single-command run); empty for a general conversational session.
	CommandName string
	// SkipBaseSkills disables BaseSkills injection for focused worker sessions
	// that receive their skills explicitly.
	SkipBaseSkills bool
	MaxPending     int
	// Loop supplies the installed reasoning algorithm.
	Loop agent.Loop
}

// Accessors lend the runtime capabilities used by host presentation.
func (rt *Runtime) Skills() *skills.Store {
	if rt == nil {
		return nil
	}
	return rt.skills
}

func (rt *Runtime) CommandRegistry() coretool.CommandExecutor {
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

func (rt *Runtime) Tools() coretool.Executor {
	if rt == nil {
		return nil
	}
	return rt.tools
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
	logger, rc := rt.Logger, rt.config
	runtimeCtx, runtimeCancel := context.WithCancel(lifetime)
	rt.ctx, rt.cancel = runtimeCtx, runtimeCancel
	rt.primarySessionID = rc.PrimarySessionID
	rt.maxPending = rc.MaxPending
	if rt.primarySessionID == "" {
		rt.primarySessionID = "task"
	}
	rt.heartbeat = rc.Heartbeat
	provider, providerConfig := rt.providers.Current()
	var resumeCounter int64
	if rc.Resume != "" {
		if rt.history == nil {
			return fmt.Errorf("session history is not installed")
		}
		data, err := rt.history.Load(ctx, rc.Resume)
		if err != nil {
			return fmt.Errorf("resume session: %w", err)
		}
		rt.resumeMessages = data.Messages
		rt.resumeSessionID = data.SessionID
		resumeCounter = data.MessageCounter
		logger.Importantf("resumed %d messages from %s", len(data.Messages), rc.Resume)
	}

	rt.nodeName = rc.NodeName
	rt.promptTarget = rc.PromptTarget
	if rt.promptTarget == "" {
		rt.promptTarget = prompt.MainSystem
	}
	rt.commandName = rc.CommandName
	skillNames := rc.SelectedSkills
	if !rc.SkipBaseSkills {
		skillNames = append(append([]string(nil), rc.BaseSkills...), skillNames...)
	}
	for _, name := range skillNames {
		if promptHasLoadedSkill(rt.loadedSkills, name) {
			continue
		}
		body := rt.skills.ReadBody(name)
		if body != "" {
			rt.loadedSkills = append(rt.loadedSkills, prompt.LoadedSkill{Name: name, Body: body})
		}
	}

	rt.agentConfig = agent.Config{
		Loop:                  rc.Loop,
		Provider:              provider,
		Tools:                 rt.tools,
		Model:                 providerConfig.Model,
		MaxTokens:             providerConfig.MaxTokens,
		ContextWindow:         providerConfig.ContextWindow,
		Logger:                logger,
		CacheRetention:        agent.CacheShort,
		Bus:                   rt.events,
		Hooks:                 rt.hooks,
		CaptureProviderFrames: rc.CaptureProviderFrames,
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
		NodeName: rt.nodeName, CommandName: rt.commandName,
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
		input.Agent.CommandDocs = rt.commandRegistry.UsageDocs()
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
		rt.Logger.Warnf("prompt contribution=%q section=%q: %s", diagnostic.Contribution, diagnostic.Section, diagnostic.Message)
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

// Active reports whether the runtime accepts work.
func (rt *Runtime) Active() bool {
	if rt == nil {
		return false
	}
	rt.lifecycle.Lock()
	defer rt.lifecycle.Unlock()
	return rt.loaded && !rt.closing
}

// ProviderState is the model this runtime reasons with, and its configuration.
func (rt *Runtime) ProviderState() (agent.Provider, agent.ProviderConfig) {
	if rt == nil || rt.providers == nil {
		return nil, agent.ProviderConfig{}
	}
	return rt.providers.Current()
}

// ProviderFallbacks are the configured alternatives to the active model.
func (rt *Runtime) ProviderFallbacks() []provider.Entry {
	if rt == nil || rt.providers == nil {
		return nil
	}
	return rt.providers.Fallbacks()
}

// Context ends when the runtime shuts down. It is nil before Load.
func (rt *Runtime) Context() context.Context { return rt.ctx }
