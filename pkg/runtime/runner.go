package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	cmdpkg "github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

// ---------------------------------------------------------------------------
// AgentRuntime — unified factory for all agent execution modes
// ---------------------------------------------------------------------------

type AgentRuntime struct {
	primarySessionID   string
	app                *apppkg.App
	nodeName           string
	systemPrompt       string
	heartbeat          time.Duration
	config             agent.Config
	resumeMessages     []*aop.Message
	resumeSessionID    string
	recordPath         string
	ctx                context.Context
	cancel             context.CancelFunc
	providerMu         sync.Mutex
	mu                 sync.RWMutex
	sessions           map[string]*sessionState
	runs               map[string]*Run
	requestSeq         uint64
	closeOnce          sync.Once
	wg                 sync.WaitGroup
	operations         sync.WaitGroup
	maxPending         int
	ownsApp            bool
	unsubscribeHandoff func()
}

func ResolveJSONLRecordPath(option *cfg.Option) string {
	if option == nil {
		return ""
	}
	if path := strings.TrimSpace(option.Resume); path != "" {
		return path
	}
	if path := strings.TrimSpace(option.OutputFile); path != "" {
		return path
	}
	if !option.SaveSession {
		return ""
	}
	name := "session-" + time.Now().Format("20060102-150405.000000000") + ".jsonl"
	return filepath.Join(cfg.DataSubDir("sessions"), name)
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

func validateFreshJSONLOutput(option *cfg.Option) error {
	if option == nil || strings.TrimSpace(option.Resume) != "" || strings.TrimSpace(option.OutputFile) == "" {
		return nil
	}
	info, err := os.Stat(option.OutputFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat AOP JSONL output %s: %w", option.OutputFile, err)
	}
	if info.IsDir() {
		return fmt.Errorf("AOP JSONL output %s is a directory", option.OutputFile)
	}
	if info.Size() > 0 {
		return fmt.Errorf("AOP JSONL output %s already exists and is not empty; use --resume to append an existing session", option.OutputFile)
	}
	return nil
}

type RuntimeConfig struct {
	PrimarySessionID string
	ExistingApp      *apppkg.App
	IOA              *apppkg.IOAConfig
	PromptConfig     *PromptConfig
	ProviderOptional bool
	MaxPending       int
}

const baseAgentSkillName = "aiscan"

func New(ctx context.Context, option *cfg.Option, logger telemetry.Logger, rc *RuntimeConfig) (*AgentRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	if option == nil {
		runtimeCancel()
		return nil, fmt.Errorf("agent runtime option is required")
	}
	if err := validateFreshJSONLOutput(option); err != nil {
		runtimeCancel()
		return nil, err
	}
	rt := &AgentRuntime{
		ctx:      runtimeCtx,
		cancel:   runtimeCancel,
		sessions: make(map[string]*sessionState),
		runs:     make(map[string]*Run),
	}
	if rc != nil {
		rt.primarySessionID = rc.PrimarySessionID
		rt.maxPending = rc.MaxPending
	}
	if rt.primarySessionID == "" {
		rt.primarySessionID = "task"
	}
	rt.heartbeat = time.Duration(option.Heartbeat) * time.Minute
	recordPath := ResolveJSONLRecordPath(option)
	if recordPath != "" {
		option.OutputFile = recordPath
		rt.recordPath = recordPath
	}
	if rc != nil && rc.ExistingApp != nil {
		rt.app = rc.ExistingApp
	} else {
		providerOptional := rc != nil && (rc.IOA != nil || rc.ProviderOptional)
		appCfg := apppkg.AppConfig(option, apppkg.RuntimeFeatures{
			ProviderEnabled:  true,
			ProviderOptional: providerOptional,
			ToolsEnabled:     true,
			AIEnabled:        true,
		}, logger)
		if rc != nil && rc.IOA != nil {
			appCfg.IOA = rc.IOA
		}
		application, err := apppkg.New(ctx, appCfg)
		if err != nil {
			runtimeCancel()
			return nil, fmt.Errorf("init app: %w", err)
		}
		rt.app = application
		rt.ownsApp = true
		_, resolvedConfig := application.ProviderState()
		apppkg.ApplyResolvedProviderOptions(option, resolvedConfig)

		for _, d := range application.SkillDiagnostics {
			logger.Warnf("skill %s: %s", d.Path, d.Message)
		}

		if rc == nil || rc.IOA == nil {
			if err := registerIOATools(ctx, application, option); err != nil {
				application.Close()
				runtimeCancel()
				return nil, fmt.Errorf("init ioa tools: %w", err)
			}
		}
	}
	provider, providerConfig := rt.app.ProviderState()
	if rt.app != nil {
		rt.app.SetLogger(logger)
		logger = rt.app.Logger()
	}
	publicBus := rt.app.EventBus
	if publicBus == nil {
		rt.Close()
		return nil, fmt.Errorf("application event bus is required")
	}
	if recordPath != "" {
		if err := rt.app.StartRecording(recordPath); err != nil {
			rt.Close()
			return nil, fmt.Errorf("open JSONL recorder: %w", err)
		}
		logger.Importantf("recording session JSONL to %s", recordPath)
	}
	var resumeCounter int64
	if option.Resume != "" {
		data, err := ReadHistory(option.Resume)
		if err != nil {
			rt.Close()
			return nil, fmt.Errorf("resume session: %w", err)
		}
		rt.resumeMessages = data.Messages
		rt.resumeSessionID = data.SessionID
		resumeCounter = data.MessageCounter
		logger.Importantf("resumed %d messages from %s", len(data.Messages), option.Resume)
	}

	nodeName := ResolveIOANodeName(option)
	rt.nodeName = nodeName

	pc := &PromptConfig{
		Tools:       rt.app.Commands,
		ScannerDocs: rt.app.Commands.UsageDocs(),
		Skills:      rt.app.Skills.Skills,
		NodeName:    nodeName,
		Space:       option.Space,
	}
	if rc != nil && rc.PromptConfig != nil {
		promptConfig := *rc.PromptConfig
		promptConfig.LoadedSkills = append([]LoadedSkill(nil), rc.PromptConfig.LoadedSkills...)
		pc = &promptConfig
	}
	skillNames := option.Skills
	if !pc.ScannerAgentMode {
		skillNames = append([]string{baseAgentSkillName}, skillNames...)
	}
	for _, name := range skillNames {
		if promptHasLoadedSkill(pc, name) {
			continue
		}
		body := rt.app.Skills.ReadBody(name)
		if body == "" {
			body = skills.ReadFile("skills/" + name + ".md")
		}
		if body == "" {
			body = skills.ReadFile(name)
		}
		if body != "" {
			pc.LoadedSkills = append(pc.LoadedSkills, LoadedSkill{Name: name, Body: body})
		}
	}
	rt.systemPrompt = BuildSystemPrompt(pc, nil)
	logger.Debugf("system prompt length: %d chars", len(rt.systemPrompt))

	rt.config = agent.Config{
		Provider:              provider,
		Tools:                 rt.app.Commands,
		Model:                 providerConfig.Model,
		MaxTokens:             providerConfig.MaxTokens,
		ContextWindow:         providerConfig.ContextWindow,
		Logger:                logger,
		CacheRetention:        agent.CacheShort,
		Bus:                   rt.app,
		Hooks:                 rt.app.Hooks,
		CaptureProviderFrames: option.CaptureProviderFrames,
		MessageCounter:        resumeCounter,
	}

	subAgentTool := agent.NewSubAgentTool(func(name string) (agent.AgentType, error) {
		if rt.app.Skills == nil {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		s, ok := rt.app.Skills.ByName(name)
		if !ok {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		if !s.Agent {
			return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
		}
		return agent.AgentType{
			FormattedPrompt: rt.app.Skills.FormatInvocation(s, ""),
			Model:           s.AgentModel,
			Background:      s.AgentBackground,
		}, nil
	})
	ioaSpace := option.Space
	if ioaSpace == "" && rc != nil && rc.IOA != nil {
		ioaSpace = rc.IOA.Space
	}
	rt.unsubscribeHandoff = subscribeIOAHandoffContext(rt.ctx, publicBus, rt.app.IOAClient, ioaSpace, logger)
	rt.app.Commands.RegisterTool(subAgentTool)
	loop := newLoopCommand()
	rt.app.Commands.Register(cmdpkg.Command{
		Name: loop.Name(), Usage: loop.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/loop.md",
		Run:             loop.Run,
	}, "loop")

	if !isNilIOADependency(rt.app.IOAStreamClient) && option.Space != "" {
		nodeID := ""
		if rt.app.IOAClient != nil {
			nodeID = rt.app.IOAClient.NodeID()
		}
		spaceInfo, err := rt.app.IOAStreamClient.Space(rt.ctx, option.Space, "aiscan agent")
		if err != nil {
			logger.Warnf("ioa space resolve: %s", err)
		} else {
			rt.wg.Add(1)
			telemetry.SafeGo("ioa-space-subscription", func() {
				defer rt.wg.Done()
				subscribeIOASpace(rt.ctx, rt.app.IOAStreamClient, spaceInfo.ID, nodeID, rt.pushAsync, logger)
			})
		}
	}

	return rt, nil
}

func promptHasLoadedSkill(pc *PromptConfig, name string) bool {
	for _, loaded := range pc.LoadedSkills {
		if loaded.Name == name {
			return true
		}
	}
	return false
}

func (rt *AgentRuntime) Close() {
	if rt == nil {
		return
	}
	rt.closeOnce.Do(func() {
		if rt.cancel != nil {
			rt.cancel()
		}
		rt.mu.RLock()
		ids := make([]string, 0, len(rt.sessions))
		for id := range rt.sessions {
			ids = append(ids, id)
		}
		rt.mu.RUnlock()
		for _, id := range ids {
			_ = rt.CloseSession(context.Background(), id, SessionCloseRuntime)
		}
		rt.wg.Wait()
		rt.operations.Wait()
		if rt.unsubscribeHandoff != nil {
			rt.unsubscribeHandoff()
		}
		if rt.ownsApp && rt.app != nil {
			rt.app.Close()
		}
	})
}

func (rt *AgentRuntime) SetLogger(logger telemetry.Logger) {
	if rt == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if rt.app != nil {
		rt.app.SetLogger(logger)
		logger = rt.app.Logger()
	}
	rt.mu.Lock()
	rt.config.Logger = logger
	for _, sess := range rt.sessions {
		sess.agent.SetLogger(logger)
	}
	rt.mu.Unlock()
}

// ReloadProvider rebuilds application configuration and updates this Runtime's
// template and existing sessions. In-flight runs retain their snapshot.
func (rt *AgentRuntime) ReloadProvider(option *cfg.Option) (agent.Provider, string, error) {
	if option == nil {
		return nil, "", fmt.Errorf("provider option is required")
	}
	provider, resolved, err := rt.reloadProvider(apppkg.ProviderConfig(option))
	return provider, resolved.Model, err
}

func (rt *AgentRuntime) reloadProvider(config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) {
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

// SetProvider atomically updates the runtime template and every existing
// conversation session. Runs already in flight keep their provider snapshot.
func (rt *AgentRuntime) SetProvider(provider agent.Provider, providerConfig agent.ProviderConfig) {
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

func (rt *AgentRuntime) applyProvider(provider agent.Provider, providerConfig agent.ProviderConfig) {
	rt.mu.Lock()
	rt.config.Provider = provider
	if providerConfig.Model != "" {
		rt.config.Model = providerConfig.Model
	}
	rt.config.MaxTokens = providerConfig.MaxTokens
	rt.config.ContextWindow = providerConfig.ContextWindow
	for _, sess := range rt.sessions {
		sess.agent.SetProviderConfig(provider, providerConfig)
	}
	rt.mu.Unlock()
}

// App returns the concrete application used by this runtime.
func (rt *AgentRuntime) App() *apppkg.App { return rt.app }

// Context ends when the runtime shuts down.
func (rt *AgentRuntime) Context() context.Context { return rt.ctx }
