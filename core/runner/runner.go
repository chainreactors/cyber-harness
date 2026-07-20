package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
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
	Bus            *eventbus.Bus[agent.Event]
	Output         *tui.AgentOutput
	ConfigFile     string
	ResumeMessages []agent.ChatMessage
	ownsApp        bool
	cleanup        func()
}

type RuntimeConfig struct {
	ExistingApp       *App
	IOA               *cfg.IOAConfig
	PromptConfig      *PromptConfig
	NoOutput          bool
	InteractiveOutput bool
	ProviderOptional  bool
}

func NewAgentRuntime(ctx context.Context, option *cfg.Option, logger telemetry.Logger, rc *RuntimeConfig) (*AgentRuntime, error) {
	rt := &AgentRuntime{}
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

	agentBus := eventbus.New[agent.Event]()
	if rt.Output != nil {
		agentBus.Subscribe(rt.Output.HandleEvent)
	}
	rt.Bus = agentBus

	ib := inboxpkg.NewBuffered(agent.DefaultInboxCapacity)

	var ioaCancel func()
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
			go subscribeIOASpace(ioaCtx, rt.App.IOAStreamClient, spaceInfo.ID, nodeID, ib, logger)
		}
	}

	sessMgr, bashTool := bashToolAndManager(rt.App.Commands)
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
			if err := ib.Push(msg); err != nil {
				logger.Warnf("inbox push session completion: %s", err)
			}
		})
	}

	scheduler := agent.NewLoopScheduler(ib, logger)

	if option.Heartbeat > 0 {
		_, _ = scheduler.Add(ctx, agent.LoopEntry{
			Name:     "heartbeat",
			Interval: time.Duration(option.Heartbeat) * time.Minute,
			Mode:     agent.ModeInbox,
			Prompt:   "Heartbeat: review current context, check on any running sessions, and decide if action is needed.",
		})
	}

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

	parentAgent := agent.NewAgent(rt.Config)
	subAgentTool := agent.NewSubAgentTool(parentAgent, ib, func(name string) (agent.AgentType, error) {
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
	rt.App.Commands.RegisterTool(subAgentTool)
	rt.App.Commands.Register(agent.NewLoopCommand(scheduler, cmdpkg.Output), "loop")

	if option.Resume != "" {
		path := option.Resume
		data, err := agent.LoadSession(path)
		if err != nil {
			return nil, fmt.Errorf("resume session: %w", err)
		}
		rt.ResumeMessages = data.Messages
		logger.Importantf("resumed %d messages from %s", len(data.Messages), path)
	}

	if option.SaveSession {
		sessDir := cfg.DataSubDir("sessions")
		agentBus.Subscribe(func(ev agent.Event) {
			if ev.Type != agent.EventAgentEnd || len(ev.Messages) == 0 {
				return
			}
			if err := agent.SaveSession(sessDir, &agent.SessionData{
				Model:    option.Model,
				Provider: option.Provider,
				Messages: ev.Messages,
			}); err != nil {
				logger.Warnf("save session: %s", err)
			}
		})
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

	return rt, nil
}

func (rt *AgentRuntime) Close() {
	if rt.cleanup != nil {
		rt.cleanup()
	}
	if rt.ownsApp && rt.App != nil {
		rt.App.Close()
	}
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
	rt.Config.Logger = logger
	if rt.Config.LoopScheduler != nil {
		rt.Config.LoopScheduler.SetLogger(logger)
	}
	if sl, ok := rt.Config.Tools.(interface{ SetLogger(telemetry.Logger) }); ok {
		sl.SetLogger(logger)
	}
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
	rt.App.Provider = provider
	rt.App.ProviderConfig = *resolved
	rt.Config.Provider = provider
	rt.Config.Model = resolved.Model
	return provider, resolved.Model, nil
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
		result, err = a.Run(ctx, task)
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
	rt, err := NewAgentRuntime(ctx, option, logger, &RuntimeConfig{InteractiveOutput: true})
	if err != nil {
		return err
	}
	defer rt.Close()

	if _, err := cfg.ApplySelectedSkills("", option.Skills, rt.App.Skills); err != nil {
		return err
	}

	session := agent.NewAgent(rt.Config.
		WithSystemPrompt(rt.SystemPrompt).
		WithStream(true))
	if len(rt.ResumeMessages) > 0 {
		session.LoadMessages(rt.ResumeMessages)
	}

	repl := tui.NewAgentConsole(ctx, option, tui.AppInfo{
		Provider:          rt.App.Provider,
		ProviderConfig:    rt.App.ProviderConfig,
		ProviderFallbacks: rt.App.ProviderFallbacks,
		Commands:          rt.App.Commands,
		Skills:            rt.App.Skills,
		OnProviderChange: func(provider agent.Provider, providerConfig agent.ProviderConfig) {
			rt.App.Provider = provider
			rt.App.ProviderConfig = providerConfig
			rt.Config.Provider = provider
			rt.Config.Model = providerConfig.Model
		},
		OnLoggerChange: rt.SetLogger,
	}, session, rt.Output, rt.Bus)
	repl.SetOnExit(rt.Close)
	if setInterrupt != nil {
		setInterrupt(repl.InterruptCurrentRun)
	}
	return repl.Start()
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
	var stream io.Writer
	streaming := ShouldStreamScannerOutput(scannerArgs)
	if streaming {
		stream = os.Stdout
	}
	out, err := application.Commands.ExecuteArgsStreaming(ctx, scannerArgs, stream)
	if err != nil {
		return err
	}
	if !streaming {
		fmt.Print(out)
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
	maxRounds := option.EvalMaxRetries
	if maxRounds <= 0 {
		maxRounds = 3
	}
	return evaluator.EvalLoopConfig{
		Evaluator: evaluator.New(evaluator.Config{
			Provider: rt.App.Provider,
			Model:    model,
			Logger:   logger,
		}),
		MaxEvalRounds: maxRounds,
		Goal:          task,
		Criteria:      option.EvalCriteria,
		Bus:           rt.Bus,
	}
}

// ---------------------------------------------------------------------------
// IOA inbox subscription
// ---------------------------------------------------------------------------

func subscribeIOASpace(ctx context.Context, stream ioaclient.StreamAPI, spaceID, nodeID string, ib *inboxpkg.Buffered, logger telemetry.Logger) {
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
				if err := ib.Push(m); err != nil {
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
