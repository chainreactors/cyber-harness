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
	App            *App
	NodeName       string
	SystemPrompt   string
	Option         *cfg.Option
	Config         agent.Config
	Bus            *eventbus.Bus[aop.Event]
	Output         *tui.AgentOutput
	ConfigFile     string
	ResumeMessages []agent.ChatMessage
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.RWMutex
	sessions       map[string]*sessionState
	requests       map[string]runtimeRequestState
	requestSeq     uint64
	closeOnce      sync.Once
	wg             sync.WaitGroup
	ptyManager     *tmuxpkg.Manager
	replMode       REPLMode
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
}

func NewAgentRuntime(ctx context.Context, option *cfg.Option, logger telemetry.Logger, rc *RuntimeConfig) (*AgentRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	rt := &AgentRuntime{
		ctx:      runtimeCtx,
		cancel:   runtimeCancel,
		sessions: make(map[string]*sessionState),
		requests: make(map[string]runtimeRequestState),
	}
	if rc != nil {
		rt.replMode = rc.REPLMode
	}
	if option != nil {
		optCopy := *option
		rt.Option = &optCopy
		rt.ConfigFile = option.ConfigFile
	}

	if rc != nil && rc.ExistingApp != nil {
		rt.App = rc.ExistingApp
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
		rt.App = application
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
	if rt.App != nil {
		rt.App.SetLogger(logger)
		logger = rt.App.Logger()
	}

	nodeName := ResolveIOANodeName(option)
	rt.NodeName = nodeName

	pc := &PromptConfig{
		Tools:       rt.App.Commands,
		ScannerDocs: rt.App.Commands.UsageDocs(),
		Skills:      rt.App.Skills.Skills,
		NodeName:    nodeName,
		Space:       option.Space,
	}
	for _, name := range option.Skills {
		body := rt.App.Skills.ReadBody(name)
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
	rt.SystemPrompt = BuildSystemPrompt(pc, nil)
	logger.Debugf("system prompt length: %d chars", len(rt.SystemPrompt))

	if rc == nil || !rc.NoOutput {
		if rc != nil && rc.InteractiveOutput {
			rt.Output = tui.NewAgentOutput(option)
		} else {
			rt.Output = tui.NewStaticAgentOutput(option)
		}
	}

	agentBus := eventbus.New[aop.Event]()
	if rt.Output != nil {
		agentBus.Subscribe(rt.Output.HandleEvent)
	}
	rt.Bus = agentBus

	ib := inboxpkg.NewBuffered(agent.DefaultInboxCapacity)

	var ioaCancel func()

	sessMgr, bashTool := bashToolAndManager(rt.App.Commands)
	rt.ptyManager = sessMgr
	if bashTool != nil {
		bashTool.SetInbox(ib)
	}
	if sessMgr != nil {
		sessMgr.SetOnDone(func(info tmuxpkg.Info) {
			tail := sessMgr.PeekOrEmpty(info.ID, 20)
			msg := inboxpkg.NewMessage(inboxpkg.OriginSession, "user",
				tmuxpkg.FormatCompletion(info, tail))
			msg.Meta = map[string]any{
				"session_id":   info.ID,
				"session_name": info.Name,
				"exit_code":    info.ExitCode,
			}
			if err := rt.inboxPush()(msg); err != nil {
				logger.Warnf("inbox push session completion: %s", err)
			}
		})
	}

	scheduler := agent.NewLoopScheduler(ib, logger)

	rt.Config = agent.Config{
		Provider:       rt.App.Provider,
		Fallbacks:      rt.App.ProviderFallbacks,
		Tools:          rt.App.Commands,
		Model:          option.Model,
		Logger:         logger,
		Inbox:          ib,
		LoopScheduler:  scheduler,
		CacheRetention: agent.CacheShort,
		Bus:            agentBus,
	}

	if option.SaveSession {
		sessDir := cfg.DataSubDir("sessions")
		rt.Config = rt.Config.WithOnRunEnd(func(result *agent.Result) {
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
		if rt.App.Skills == nil {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		s, ok := rt.App.Skills.ByName(name)
		if !ok {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		if !s.Agent {
			return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
		}
		return agent.AgentType{
			FormattedPrompt: rt.App.Skills.FormatInvocation(s, ""),
			Model:           s.AgentModel,
			Background:      s.AgentBackground,
		}, nil
	})
	ioaSpace := option.Space
	if ioaSpace == "" && rc != nil && rc.IOA != nil {
		ioaSpace = rc.IOA.Space
	}
	subscribeIOAHandoff(agentBus, rt.App.IOAClient, ioaSpace, logger)
	rt.App.Commands.RegisterTool(subAgentTool)
	loop := agent.NewLoopCommand(scheduler)
	rt.App.Commands.Register(cmdpkg.Command{Name: loop.Name(), Usage: loop.Usage(), Run: loop.Run}, "loop")

	if option.Resume != "" {
		path := option.Resume
		data, err := agent.LoadSession(path)
		if err != nil {
			return nil, fmt.Errorf("resume session: %w", err)
		}
		rt.ResumeMessages = data.Messages
		logger.Importantf("resumed %d messages from %s", len(data.Messages), path)
	}

	if rt.App.IOAStreamClient != nil && option.Space != "" {
		nodeID := ""
		if rt.App.IOAClient != nil {
			nodeID = rt.App.IOAClient.NodeID()
		}
		spaceInfo, err := rt.App.IOAStreamClient.Space(ctx, option.Space, "aiscan agent")
		if err != nil {
			logger.Warnf("ioa space resolve: %s", err)
		} else {
			ioaCtx, cancel := context.WithCancel(ctx)
			ioaCancel = cancel
			go subscribeIOASpace(ioaCtx, rt.App.IOAStreamClient, spaceInfo.ID, nodeID, rt.inboxPush(), logger)
		}
	}

	rt.cleanup = func() {
		if ioaCancel != nil {
			ioaCancel()
		}
		scheduler.Stop()
		if sessMgr != nil {
			sessMgr.Shutdown()
		}
	}

	// A persistent REPL is transport-owned and must survive remote detach. The
	// ephemeral local REPL is started directly by AttachLocalREPL so readline
	// control sequences are never buffered and replayed as PTY logs.
	if rt.replMode == REPLPersistent {
		if err := rt.startMainREPL(); err != nil {
			runtimeCancel()
			rt.cleanup()
			if rt.ownsApp && rt.App != nil {
				rt.App.Close()
			}
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
		rt.cancelAllRequests()
		rt.wg.Wait()
		rt.closeSessions()
		if rt.cleanup != nil {
			rt.cleanup()
		}
		if rt.ownsApp && rt.App != nil {
			rt.App.Close()
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
	if rt.App != nil {
		rt.App.SetLogger(logger)
		logger = rt.App.Logger()
	}
	rt.mu.Lock()
	rt.Config.Logger = logger
	if rt.Config.LoopScheduler != nil {
		rt.Config.LoopScheduler.SetLogger(logger)
	}
	if sl, ok := rt.Config.Tools.(interface{ SetLogger(telemetry.Logger) }); ok {
		sl.SetLogger(logger)
	}
	for _, sess := range rt.sessions {
		sess.agent.SetLogger(logger)
	}
	rt.mu.Unlock()
}

// ReloadProvider rebuilds the LLM provider from option and hot-swaps it into the
// running runtime: rt.App (used by the REPL and scan paths) and rt.Config (the
// template every new chat agent is cloned from). It returns the live provider
// and resolved model so callers can propagate the swap to already-running
// agents. On a build failure the runtime is left untouched and the error is
// returned, so a bad config push never knocks out a working provider.
func (rt *AgentRuntime) ReloadProvider(option *cfg.Option) (agent.Provider, string, error) {
	if rt == nil || rt.App == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	logger := rt.Config.Logger
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
	if rt.App != nil {
		rt.App.Provider = provider
		rt.App.ProviderConfig = providerConfig
	}
	rt.Config.Provider = provider
	if providerConfig.Model != "" {
		rt.Config.Model = providerConfig.Model
	}
	for _, sess := range rt.sessions {
		sess.agent.SetProvider(provider, providerConfig.Model)
	}
	rt.mu.Unlock()
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

	task = skills.ExpandCommand(task, rt.App.Skills)
	task, err = cfg.ApplySelectedSkills(task, option.Skills, rt.App.Skills)
	if err != nil {
		return err
	}

	if rt.Output != nil {
		rt.Output.Start("task", task)
	}

	a := agent.NewAgent(rt.Config.
		WithSystemPrompt(rt.SystemPrompt).
		WithStream(true))
	if len(rt.ResumeMessages) > 0 {
		a.LoadMessages(rt.ResumeMessages)
	}

	var result *agent.Result
	if option.EvalCriteria != "" {
		evalCfg := buildEvalConfig(option, rt, logger, task)
		result, _, err = evaluator.RunWithEval(ctx, a, evalCfg)
	} else {
		result, err = a.Run(ctx, agent.TextInput(task))
	}
	if err != nil {
		return err
	}
	if rt.Output != nil && result != nil && strings.TrimSpace(result.Output) != "" {
		rt.Output.Final(result.Output)
	}
	return nil
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

	if _, err := cfg.ApplySelectedSkills("", option.Skills, rt.App.Skills); err != nil {
		return err
	}

	if setInterrupt != nil {
		setInterrupt(func() bool { return rt.CancelSession(MainREPLName) })
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
	case "scan", "gogo", "spray", "zombie", "neutron":
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
	return evaluator.NewLoopConfig(rt.App.Provider, model, logger, task, option.EvalCriteria, option.EvalMaxRetries)
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

func bashToolAndManager(reg interface {
	GetTool(string) (cmdpkg.AgentTool, bool)
}) (*tmuxpkg.Manager, *cmdpkg.BashTool) {
	if reg == nil {
		return nil, nil
	}
	tool, ok := reg.GetTool("bash")
	if !ok {
		return nil, nil
	}
	bt, ok := tool.(*cmdpkg.BashTool)
	if !ok {
		return nil, nil
	}
	return bt.Manager(), bt
}
