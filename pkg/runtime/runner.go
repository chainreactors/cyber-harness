package runtime

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
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	coretool "github.com/chainreactors/aiscan/core/tool"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	cmdpkg "github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
)

// ---------------------------------------------------------------------------
// AgentRuntime — unified factory for all agent execution modes
// ---------------------------------------------------------------------------

type AgentRuntime struct {
	option             *cfg.Option
	logger             telemetry.Logger
	runtimeConfig      RuntimeConfig
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
	closeDone          chan struct{}
	lifecycle          sync.Mutex
	loaded             bool
	closing            bool
	wg                 sync.WaitGroup
	operations         sync.WaitGroup
	maxPending         int
	unsubscribeHandoff func()
	ioa                *ioatools.Service
}

var _ extension.Extension = (*AgentRuntime)(nil)

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
	PromptConfig     *PromptConfig
	MaxPending       int
	Loop             agent.Loop
}

// IOA returns the optional collaboration module borrowed by this runtime.
// The profile remains its owner.
func (rt *AgentRuntime) IOA() *ioatools.Service {
	if rt == nil {
		return nil
	}
	return rt.ioa
}

const baseAgentSkillName = "aiscan"

// New constructs an inert Agent runtime over an application owned by the
// caller. Session subscriptions, history IO and command publication begin in
// Load.
func New(application *apppkg.App, ioa *ioatools.Service, option *cfg.Option, logger telemetry.Logger, rc RuntimeConfig) (*AgentRuntime, error) {
	if option == nil {
		return nil, fmt.Errorf("agent runtime option is required")
	}
	if application == nil {
		return nil, fmt.Errorf("agent runtime application is required")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &AgentRuntime{
		app: application, ioa: ioa, option: option, logger: logger, runtimeConfig: rc,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run), closeDone: make(chan struct{}),
	}, nil
}

// Load activates runtime-owned session work. ctx bounds initialization only;
// Close owns cancellation of the runtime lifetime.
func (rt *AgentRuntime) Load(scope *extension.Context) error {
	ctx := scope.Init()
	if rt == nil {
		return fmt.Errorf("agent runtime is required")
	}
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
	logger, rc := rt.logger, rt.runtimeConfig
	if err := validateFreshJSONLOutput(option); err != nil {
		return err
	}
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	rt.ctx, rt.cancel = runtimeCtx, runtimeCancel
	rt.primarySessionID = rc.PrimarySessionID
	rt.maxPending = rc.MaxPending
	if rt.primarySessionID == "" {
		rt.primarySessionID = "task"
	}
	rt.heartbeat = time.Duration(option.Heartbeat) * time.Minute
	recordPath := ResolveJSONLRecordPath(option)
	if recordPath != "" {
		option.OutputFile = recordPath
		rt.recordPath = recordPath
	}
	rt.app = application
	provider, providerConfig := rt.app.ProviderState()
	if rt.app != nil {
		rt.app.SetLogger(logger)
		logger = rt.app.Logger()
	}
	publicBus := rt.app.EventBus
	if publicBus == nil {
		return fmt.Errorf("application event bus is required")
	}
	if recordPath != "" {
		if err := rt.app.StartRecording(recordPath); err != nil {
			return fmt.Errorf("open JSONL recorder: %w", err)
		}
		logger.Importantf("recording session JSONL to %s", recordPath)
	}
	var resumeCounter int64
	if option.Resume != "" {
		data, err := ReadHistory(option.Resume)
		if err != nil {
			return fmt.Errorf("resume session: %w", err)
		}
		rt.resumeMessages = data.Messages
		rt.resumeSessionID = data.SessionID
		resumeCounter = data.MessageCounter
		logger.Importantf("resumed %d messages from %s", len(data.Messages), option.Resume)
	}

	nodeName := ioatools.ResolveNodeName(option.IOANodeName)
	rt.nodeName = nodeName
	executor := coretool.EmptyExecutor()
	if rt.app.Tools != nil {
		executor = rt.app.Tools
	}

	pc := &PromptConfig{
		Tools:       executor,
		ScannerDocs: rt.app.Commands.UsageDocs(),
		Skills:      rt.app.Skills.Skills,
		NodeName:    nodeName,
		Space:       option.Space,
	}
	if rc.PromptConfig != nil {
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
		Loop:                  rc.Loop,
		Provider:              provider,
		Tools:                 executor,
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

	ioaSpace := option.Space
	var ioaClient *ioaclient.Client
	var ioaStream ioaclient.StreamAPI
	if rt.ioa != nil {
		ioaClient, ioaStream = rt.ioa.Client(), rt.ioa.Stream()
	}
	rt.unsubscribeHandoff = subscribeIOAHandoffContext(rt.ctx, publicBus, ioaClient, ioaSpace, logger)
	if !rt.app.Commands.Has("loop") {
		loop := newLoopCommand()
		if err := rt.app.Commands.Register("loop", "loop", cmdpkg.Command{
			Name: loop.Name(), Usage: loop.Usage(),
			DescriptionPath: "aiscan://skills/aiscan/okf/runtime/loop.md",
			Run:             loop.Run,
		}); err != nil {
			return fmt.Errorf("register loop command: %w", err)
		}
	}
	if !isNilIOADependency(ioaStream) && option.Space != "" {
		nodeID := ""
		if ioaClient != nil {
			nodeID = ioaClient.NodeID()
		}
		spaceInfo, err := ioaStream.Space(rt.ctx, option.Space, "aiscan agent")
		if err != nil {
			logger.Warnf("ioa space resolve: %s", err)
		} else {
			rt.wg.Add(1)
			telemetry.SafeGo("ioa-space-subscription", func() {
				defer rt.wg.Done()
				subscribeIOASpace(rt.ctx, ioaStream, spaceInfo.ID, nodeID, rt.pushAsync, logger)
			})
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	rt.loaded = true
	return nil
}

// ready rejects business admission until the owning profile has completed
// Load. Lifecycle wiring such as RegisterNamespaces and Subscribe may happen
// earlier, but their handlers cannot create sessions or runs through this
// gate.
func (rt *AgentRuntime) ready() error {
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

func promptHasLoadedSkill(pc *PromptConfig, name string) bool {
	for _, loaded := range pc.LoadedSkills {
		if loaded.Name == name {
			return true
		}
	}
	return false
}

func (rt *AgentRuntime) Close(ctx context.Context) error {
	if rt == nil {
		return nil
	}
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
			rt.lifecycle.Lock()
			rt.loaded = false
			rt.lifecycle.Unlock()
		}()
	})
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
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

// Context ends when the runtime shuts down. It is nil before Load.
func (rt *AgentRuntime) Context() context.Context { return rt.ctx }
