package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/aop"
	cmdpkg "github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/skills"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

// ---------------------------------------------------------------------------
// AgentRuntime — unified factory for all agent execution modes
// ---------------------------------------------------------------------------

type AgentRuntime struct {
	app            *App
	nodeName       string
	systemPrompt   string
	option         *cfg.Option
	config         agent.Config
	bus            *eventbus.Bus[aop.Event]
	kernelBus      *eventbus.Bus[aop.Event]
	sessionEvents  *sessionEmitter
	output         *tui.AgentOutput
	configFile     string
	resumeMessages []agent.ChatMessage
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.RWMutex
	sessions       map[string]*sessionState
	turnIDs        map[string]struct{}
	requestSeq     uint64
	closeOnce      sync.Once
	wg             sync.WaitGroup
	ptyManager     *tmuxpkg.Manager
	replMode       REPLMode
	maxPending     int
	ownsApp        bool
	cleanup        func()
}

type REPLMode uint8

const (
	REPLDisabled REPLMode = iota
	REPLEphemeral
	REPLPersistent
)

type RuntimeConfig struct {
	ExistingApp       *App
	IOA               *cfg.IOAConfig
	PromptConfig      *PromptConfig
	NoOutput          bool
	InteractiveOutput bool
	ProviderOptional  bool
	REPLMode          REPLMode
	MaxPending        int
}

func NewAgentRuntime(ctx context.Context, option *cfg.Option, logger telemetry.Logger, rc *RuntimeConfig) (*AgentRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	if option == nil {
		runtimeCancel()
		return nil, fmt.Errorf("agent runtime option is required")
	}
	rt := &AgentRuntime{
		ctx:      runtimeCtx,
		cancel:   runtimeCancel,
		sessions: make(map[string]*sessionState),
		turnIDs:  make(map[string]struct{}),
	}
	if rc != nil {
		rt.replMode = rc.REPLMode
		rt.maxPending = rc.MaxPending
	}
	if option != nil {
		optCopy := *option
		rt.option = &optCopy
		rt.configFile = option.ConfigFile
	}

	if rc != nil && rc.ExistingApp != nil {
		rt.app = rc.ExistingApp
	} else {
		providerOptional := rc != nil && (rc.IOA != nil || rc.ProviderOptional)
		appCfg := cfg.AppConfig(option, cfg.RuntimeFeatures{
			ProviderEnabled:  true,
			ProviderOptional: providerOptional,
			ToolsEnabled:     true,
			AIEnabled:        true,
		}, logger)
		if rc != nil && rc.IOA != nil {
			appCfg.IOA = rc.IOA
		}
		application, err := NewApp(ctx, appCfg)
		if err != nil {
			return nil, fmt.Errorf("init app: %w", err)
		}
		rt.app = application
		rt.ownsApp = true
		cfg.ApplyResolvedProviderOptions(option, application.ProviderConfig)

		for _, d := range application.SkillDiagnostics {
			logger.Warnf("skill %s: %s", d.Path, d.Message)
		}

		if rc == nil || rc.IOA == nil {
			if err := registerIOATools(ctx, application, option); err != nil {
				application.Close()
				return nil, fmt.Errorf("init ioa tools: %w", err)
			}
		}
	}
	if rt.app != nil {
		if rt.option != nil {
			if rt.app.ProviderConfig.Model != "" {
				rt.option.Model = rt.app.ProviderConfig.Model
			}
			rt.option.MaxTokens = rt.app.ProviderConfig.MaxTokens
			rt.option.ContextWindow = rt.app.ProviderConfig.ContextWindow
		}
		rt.app.SetLogger(logger)
		logger = rt.app.Logger()
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
	for _, name := range option.Skills {
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
	if rc != nil && rc.PromptConfig != nil {
		pc = rc.PromptConfig
	}
	rt.systemPrompt = BuildSystemPrompt(pc, nil)
	logger.Debugf("system prompt length: %d chars", len(rt.systemPrompt))

	if rc == nil || !rc.NoOutput {
		if rc != nil && rc.InteractiveOutput {
			rt.output = tui.NewAgentOutput(option)
		} else {
			rt.output = tui.NewStaticAgentOutput(option)
		}
	}

	publicBus := eventbus.New[aop.Event]()
	if rt.output != nil {
		publicBus.Subscribe(rt.output.HandleEvent)
	}
	rt.bus = publicBus
	rt.kernelBus = eventbus.New[aop.Event]()
	rt.sessionEvents = newSessionEmitter(publicBus)
	rt.kernelBus.Subscribe(rt.sessionEvents.emit)

	var ioaCancel func()
	var handoffCancel func()

	sessMgr := bashManager(rt.app.Commands)
	rt.ptyManager = sessMgr
	rt.cleanup = func() {
		if handoffCancel != nil {
			handoffCancel()
		}
		if ioaCancel != nil {
			ioaCancel()
		}
		if sessMgr != nil {
			sessMgr.Shutdown()
		}
	}

	rt.config = agent.Config{
		Provider:       rt.app.Provider,
		Tools:          rt.app.Commands,
		Model:          rt.app.ProviderConfig.Model,
		MaxTokens:      rt.app.ProviderConfig.MaxTokens,
		ContextWindow:  rt.app.ProviderConfig.ContextWindow,
		Logger:         logger,
		CacheRetention: agent.CacheShort,
		Bus:            rt.kernelBus,
	}

	if option.SaveSession {
		sessDir := cfg.DataSubDir("sessions")
		rt.config = rt.config.WithOnRunEnd(func(result *agent.Result) {
			if result == nil || len(result.Messages) == 0 {
				return
			}
			if err := agent.SaveSession(sessDir, &agent.SessionData{
				Model:          option.Model,
				Provider:       option.Provider,
				Messages:       result.Messages,
				MessageCounter: result.MessageCounter,
			}); err != nil {
				logger.Warnf("save session: %s", err)
			}
		})
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
	handoffCancel = subscribeIOAHandoffContext(rt.ctx, publicBus, rt.app.IOAClient, ioaSpace, logger)
	rt.app.Commands.RegisterTool(subAgentTool)
	loop := agent.NewLoopCommand()
	rt.app.Commands.Register(cmdpkg.Command{Name: loop.Name(), Usage: loop.Usage(), Run: loop.Run}, "loop")

	if option.Resume != "" {
		path := option.Resume
		data, err := agent.LoadSession(path)
		if err != nil {
			rt.Close()
			return nil, fmt.Errorf("resume session: %w", err)
		}
		rt.resumeMessages = data.Messages
		logger.Importantf("resumed %d messages from %s", len(data.Messages), path)
	}

	if rt.app.IOAStreamClient != nil && option.Space != "" {
		nodeID := ""
		if rt.app.IOAClient != nil {
			nodeID = rt.app.IOAClient.NodeID()
		}
		spaceInfo, err := rt.app.IOAStreamClient.Space(ctx, option.Space, "aiscan agent")
		if err != nil {
			logger.Warnf("ioa space resolve: %s", err)
		} else {
			ioaCtx, cancel := context.WithCancel(ctx)
			ioaCancel = cancel
			go subscribeIOASpace(ioaCtx, rt.app.IOAStreamClient, spaceInfo.ID, nodeID, rt.pushAsync, logger)
		}
	}

	// A persistent REPL is transport-owned and must survive remote detach. The
	// ephemeral local REPL is started directly by AttachLocalREPL so readline
	// control sequences are never buffered and replayed as PTY logs.
	if rt.replMode == REPLPersistent {
		if err := rt.startMainREPL(); err != nil {
			rt.Close()
			return nil, fmt.Errorf("start main repl: %w", err)
		}
	}

	return rt, nil
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
		if rt.cleanup != nil {
			rt.cleanup()
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
	if sl, ok := rt.config.Tools.(interface{ SetLogger(telemetry.Logger) }); ok {
		sl.SetLogger(logger)
	}
	for _, sess := range rt.sessions {
		sess.agent.SetLogger(logger)
	}
	rt.mu.Unlock()
}

// ReloadProvider rebuilds the LLM provider from option and hot-swaps it into the
// running runtime application and session template. It returns the live provider
// template every new chat agent is cloned from). It returns the live provider
// and resolved model so callers can propagate the swap to already-running
// agents. On a build failure the runtime is left untouched and the error is
// returned, so a bad config push never knocks out a working provider.
func (rt *AgentRuntime) ReloadProvider(option *cfg.Option) (agent.Provider, string, error) {
	if rt == nil || rt.app == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	logger := rt.config.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	provider, resolved, err := initProvider(cfg.ProviderConfig(option), logger)
	if err != nil {
		return nil, "", err
	}
	rt.SetProvider(provider, *resolved)
	return provider, resolved.Model, nil
}

// SetProvider atomically updates the runtime template and every existing
// conversation session. Runs already in flight keep their provider snapshot.
func (rt *AgentRuntime) SetProvider(provider agent.Provider, providerConfig agent.ProviderConfig) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	if rt.app != nil {
		rt.app.Provider = provider
		rt.app.ProviderConfig = providerConfig
	}
	rt.config.Provider = provider
	if providerConfig.Model != "" {
		rt.config.Model = providerConfig.Model
	}
	rt.config.MaxTokens = providerConfig.MaxTokens
	rt.config.ContextWindow = providerConfig.ContextWindow
	for _, sess := range rt.sessions {
		sess.agent.SetProviderConfig(provider, providerConfig)
	}
	output := rt.output
	model := rt.config.Model
	rt.mu.Unlock()
	if output != nil {
		contextWindow := providerConfig.ContextWindow
		if contextWindow <= 0 {
			contextWindow = agent.ModelContextWindow(model)
		}
		output.SetContextWindow(contextWindow)
	}
}

// ---------------------------------------------------------------------------
// Mode dispatch
// ---------------------------------------------------------------------------

func RunAgentMode(ctx context.Context, option *cfg.Option, logger telemetry.Logger, setInterrupt ...func(func() bool)) error {
	var si func(func() bool)
	if len(setInterrupt) > 0 {
		si = setInterrupt[0]
	}
	if !cfg.HasAgentOneShotInput(option) {
		return runInteractiveMode(ctx, option, logger, si)
	}
	return runOneShotMode(ctx, option, logger)
}

// ---------------------------------------------------------------------------
// Agent one-shot
// ---------------------------------------------------------------------------

func runOneShotMode(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	task, err := cfg.ResolveTask(option)
	if err != nil {
		return err
	}

	rt, err := NewAgentRuntime(ctx, option, logger, nil)
	if err != nil {
		return err
	}
	defer rt.Close()

	task = skills.ExpandCommand(task, rt.app.Skills)
	task, err = cfg.ApplySelectedSkills(task, option.Skills, rt.app.Skills)
	if err != nil {
		return err
	}

	if rt.output != nil {
		rt.output.Start("task", task)
	}

	session, err := rt.OpenSession(ctx, SessionOptions{ID: "task", Messages: rt.resumeMessages})
	if err != nil {
		return err
	}
	run, err := session.Run(ctx, RunInput{
		Parts:    []aop.MessagePart{{Type: aop.PartText, Text: task}},
		MaxTurns: rt.config.MaxTurns, EvalCriteria: option.EvalCriteria, EvalMaxRounds: option.EvalMaxRetries,
	})
	if err != nil {
		return err
	}
	result, err := run.Wait()
	if rt.output != nil && strings.TrimSpace(result.Output) != "" {
		rt.output.Final(result.Output)
	}
	_ = rt.CloseSession(context.Background(), "task", SessionCloseCompleted)
	return err
}

// ---------------------------------------------------------------------------
// Agent interactive (REPL)
// ---------------------------------------------------------------------------

func runInteractiveMode(ctx context.Context, option *cfg.Option, logger telemetry.Logger, setInterrupt func(func() bool)) error {
	rt, err := NewAgentRuntime(ctx, option, logger, &RuntimeConfig{
		NoOutput: true,
		REPLMode: REPLEphemeral,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	if _, err := cfg.ApplySelectedSkills("", option.Skills, rt.app.Skills); err != nil {
		return err
	}

	if setInterrupt != nil {
		setInterrupt(func() bool { return false })
	}
	return rt.AttachLocalREPL(ctx)
}

// ---------------------------------------------------------------------------
// Scanner direct execution
// ---------------------------------------------------------------------------

func RunDirectScannerMode(ctx context.Context, option *cfg.Option, rest []string, logger telemetry.Logger) error {
	features, scannerArgs, err := DirectScannerRuntimeFeatures(rest)
	if err != nil {
		return err
	}
	if features.Warning != "" && !option.Quiet {
		fmt.Fprintf(os.Stderr, "warning: %s\n", features.Warning)
	}
	if option.AI || features.ScannerAI {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.ToolsEnabled = true
		features.AIEnabled = true
	}
	if cfg.IsScannerHelpRequest(scannerArgs) {
		if usage, ok := cfg.StaticScannerUsage(scannerArgs[0]); ok {
			fmt.Print(usage)
			if !strings.HasSuffix(usage, "\n") {
				fmt.Println()
			}
			return nil
		}
	}

	scannerLogger := logger
	if !directScannerDebugEnabled(option, scannerArgs) {
		scannerLogger = telemetry.ErrorOnlyLogger(logger)
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
	}

	application, err := NewApp(ctx, cfg.AppConfig(option, features, scannerLogger))
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer application.Close()
	if err := application.WaitEngines(ctx); err != nil {
		return fmt.Errorf("engine init: %w", err)
	}
	cfg.ApplyResolvedProviderOptions(option, application.ProviderConfig)

	if !application.Commands.Has(scannerArgs[0]) {
		return fmt.Errorf("unknown subcommand: %s", scannerArgs[0])
	}
	if option.Debug && scannerCommandSupportsDebug(scannerArgs[0]) && !toolargs.BoolFlagEnabled(scannerArgs[1:], "--debug") {
		scannerArgs = append(scannerArgs, "--debug")
	}

	if option.AI && scannerArgs[0] != "scan" {
		if ScannerWithAgentFunc == nil {
			return fmt.Errorf("scanner agent mode not available in this build")
		}
		return ScannerWithAgentFunc(ctx, option, application, scannerArgs, logger)
	}

	if option.NoColor && scannerArgs[0] == "scan" && !HasScannerFlag(scannerArgs[1:], "--no-color") {
		scannerArgs = append(scannerArgs, "--no-color")
	}
	tool, ok := application.Commands.GetTool("bash")
	if !ok {
		return fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*cmdpkg.BashTool)
	if !ok {
		return fmt.Errorf("registered bash tool has unexpected type")
	}
	streaming := ShouldStreamScannerOutput(scannerArgs)
	var captured strings.Builder
	execution, err := bash.RunForeground(ctx, cmdpkg.JoinCommandLine(scannerArgs[0], scannerArgs[1:]), cmdpkg.BashExecOptions{
		OnOutput: func(data []byte) {
			if streaming {
				_, _ = os.Stdout.Write(data)
			} else {
				_, _ = captured.Write(data)
			}
		},
	})
	if err != nil {
		return err
	}
	if !streaming {
		fmt.Print(captured.String())
	}
	if execution.ExitCode != 0 {
		return fmt.Errorf("%s exited with code %d", scannerArgs[0], execution.ExitCode)
	}
	return nil
}

func directScannerDebugEnabled(option *cfg.Option, scannerArgs []string) bool {
	if option != nil && option.Debug {
		return true
	}
	if len(scannerArgs) == 0 || !scannerCommandSupportsDebug(scannerArgs[0]) {
		return false
	}
	return toolargs.BoolFlagEnabled(scannerArgs[1:], "--debug")
}

func scannerCommandSupportsDebug(name string) bool {
	switch name {
	case "scan", "gogo", "spray", "zombie", "neutron", "proton":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

func buildEvalConfig(option *cfg.Option, rt *AgentRuntime, logger telemetry.Logger, task string) evaluator.EvalLoopConfig {
	model := option.Model
	if option.EvalModel != "" {
		model = option.EvalModel
	}
	return evaluator.NewLoopConfig(rt.app.Provider, model, logger, task, option.EvalCriteria, option.EvalMaxRetries)
}

// ---------------------------------------------------------------------------
// IOA inbox subscription
// ---------------------------------------------------------------------------

func subscribeIOASpace(ctx context.Context, stream ioaclient.StreamAPI, spaceID, nodeID string, push func(inboxpkg.Message) error, logger telemetry.Logger) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		msgs, errs, cancel, err := stream.Subscribe(ctx, spaceID)
		if err != nil {
			delay := agent.RetryDelay(attempt)
			logger.Debugf("ioa subscribe: %s, retry in %s", err, delay)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return
			}
		}
		attempt = 0
		logger.Debugf("ioa subscribed to space %s", spaceID)
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					goto reconnect
				}
				if msg.Sender == nodeID {
					continue
				}
				m := inboxpkg.NewMessage(inboxpkg.OriginPeer, "user", formatIOAMessage(msg))
				m.Meta = map[string]any{"sender": msg.Sender, "message_id": msg.ID}
				if err := push(m); err != nil {
					logger.Warnf("inbox push ioa: %s", err)
				}
			case <-errs:
				goto reconnect
			case <-ctx.Done():
				cancel()
				return
			}
		}
	reconnect:
		cancel()
	}
}

func formatIOAMessage(msg protocols.Message) string {
	if text, ok := msg.Content["text"].(string); ok {
		return text
	}
	data, _ := json.Marshal(msg.Content)
	return string(data)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type memoryIdentity struct{ ref protocols.NodeRef }

func (i memoryIdentity) IOABinding() protocols.IdentityBinding {
	return protocols.IdentityBinding{
		Namespace: "aiscan.memory",
		Subject:   i.ref.URI(),
	}
}

func registerIOATools(ctx context.Context, application *App, option *cfg.Option) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		return nil
	}
	ioaCfg := cfg.IOAConfig{
		URL:           ioaURL,
		NodeID:        option.IOANodeID,
		NodeName:      option.IOANodeName,
		Space:         option.Space,
		RegisterTools: true,
		AutoRegister:  true,
		NodeMeta:      map[string]any{"client": "aiscan"},
		Identity: memoryIdentity{ref: protocols.NodeRef{
			ID: protocols.NewID(), Authority: "memory://aiscan",
		}},
	}
	if ioaCfg.NodeName == "" {
		ioaCfg.NodeName = ResolveIOANodeName(option)
	}
	return application.InitIOA(ctx, ioaCfg)
}

func bashManager(reg interface {
	GetTool(string) (coretool.Tool, bool)
}) *tmuxpkg.Manager {
	if reg == nil {
		return nil
	}
	tool, ok := reg.GetTool("bash")
	if !ok {
		return nil
	}
	bt, ok := tool.(*cmdpkg.BashTool)
	if !ok {
		return nil
	}
	return bt.Manager()
}
