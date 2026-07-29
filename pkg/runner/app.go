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
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
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
	DataBus           *eventbus.Bus[output.ToolDataEvent]
	SCOSidecar        *output.SCOSidecar
	enginesReady      chan struct{}
	loggerMu          sync.RWMutex
	logger            telemetry.Logger
}

func NewApp(ctx context.Context, rc ApplicationConfig) (*App, error) {
	a := &App{}
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a.logger = logger
	logger = a.Logger()
	a.Hooks = hooks.New()
	a.Hooks.SetErrorSink(func(he *hooks.HandlerError) {
		a.Logger().Warnf("hook failed kind=%s source=%s error=%q", he.Kind, he.Source, he.Err)
	})

	a.DataBus = eventbus.New[output.ToolDataEvent]()
	a.SCOSidecar = output.NewSCOSidecar(a.DataBus, output.CSTXTransform)

	store, diagnostics := skills.LoadAll(rc.CLISkillPaths)
	a.Skills = store
	a.SkillDiagnostics = diagnostics

	if rc.Provider.Enabled {
		llmProvider, resolved, err := initProvider(rc.Provider.Config, logger)
		if err != nil {
			if !rc.Provider.Optional {
				return nil, err
			}
			logger.Debugf("provider not configured: %s", err)
		} else {
			a.Provider = llmProvider
			a.ProviderConfig = *resolved
			logLLMProbeStatus(ctx, *resolved, logger)
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

	a.Commands = initCoreCommands(rc, a.Provider, a.Skills, a.Hooks, logger)

	a.enginesReady = make(chan struct{})
	go func() {
		if ScannerInitFunc != nil && !rc.SkipEngines {
			ScannerInitFunc(ctx, a, rc, logger)
		}
		close(a.enginesReady)
	}()

	if rc.IOA != nil {
		if err := a.InitIOA(ctx, *rc.IOA); err != nil {
			a.Close()
			return nil, err
		}
	}

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
	if a.SCOSidecar != nil {
		a.SCOSidecar.Close()
	}
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

func logLLMProbeStatus(ctx context.Context, provCfg agent.ProviderConfig, logger telemetry.Logger) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	probeCtx, cancel := context.WithTimeout(ctx, startupLLMProbeTimeout)
	defer cancel()

	result, err := probe.TestLLM(probeCtx, probe.LLMProbeRequest{
		Provider: provCfg.Provider,
		BaseURL:  provCfg.BaseURL,
		APIKey:   provCfg.APIKey,
		Model:    provCfg.Model,
		Proxy:    provCfg.Proxy,
	}, "")
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %s", llmConfigLabel(provCfg.Provider, provCfg.Model), err.Error())))
		return
	}
	if !result.OK {
		logger.Warnf("%s", telemetry.StartupLine("fail", "llm", fmt.Sprintf("%s · %dms · %s", llmConfigLabel(result.Provider, result.Model), result.LatencyMs, result.Error)))
		return
	}

	logger.Infof("%s", telemetry.StartupOK("llm", fmt.Sprintf("%s · %dms", llmConfigLabel(result.Provider, result.Model), result.LatencyMs)))
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

func initCoreCommands(rc ApplicationConfig, llmProvider agent.Provider, skillStore *skills.Store, hookRegistry *hooks.Registry, logger telemetry.Logger) *commands.CommandRegistry {
	cmdReg := commands.NewRegistry()
	workDir, _ := os.Getwd()
	deps := &commands.Deps{
		WorkDir:           workDir,
		BashTimeout:       rc.Tools.BashTimeout,
		SkillStore:        skillStore,
		Provider:          llmProvider,
		Logger:            logger,
		TavilyKeys:        rc.Tools.TavilyKeys,
		PlaywrightSession: rc.Tools.PlaywrightSession,
		Hooks:             hookRegistry,
	}
	plan := capability.Select(capability.Options{
		Groups:        []string{"core", "arsenal", "search", "browser"},
		OptionalTools: rc.Tools.OptionalTools,
	})
	commands.BuildPlan(plan, deps, cmdReg)
	return cmdReg
}

func executeRegistryCommand(ctx context.Context, reg *commands.CommandRegistry, commandLine string, timeout time.Duration) (string, error) {
	tool, ok := reg.GetTool("bash")
	if !ok {
		return "", fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*commands.BashTool)
	if !ok {
		return "", fmt.Errorf("registered bash tool has unexpected type")
	}
	var output strings.Builder
	execution, err := bash.RunForeground(ctx, commandLine, commands.BashExecOptions{
		Timeout:  timeout,
		OnOutput: func(data []byte) { _, _ = output.Write(data) },
	})
	if err != nil {
		return output.String(), err
	}
	if execution.ExitCode != 0 {
		return output.String(), fmt.Errorf("command exited with code %d", execution.ExitCode)
	}
	return output.String(), nil
}

func appendDeepBrowserStep(sb *strings.Builder, name, commandLine, output string, err error) {
	sb.WriteString("\n## ")
	sb.WriteString(name)
	sb.WriteString("\nCommand: `")
	sb.WriteString(commandLine)
	sb.WriteString("`\n")
	if err != nil {
		sb.WriteString("Error: ")
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	output = strings.TrimSpace(output)
	if output != "" {
		if tr := truncate.Head(output, truncate.Options{}); tr.Truncated {
			sb.WriteString(tr.Content)
			sb.WriteString(fmt.Sprintf("\n[step truncated: %d/%d lines]", tr.OutputLines, tr.TotalLines))
		} else {
			sb.WriteString(tr.Content)
		}
		sb.WriteString("\n")
	}
}

func quoteCommandArg(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\r\n'\"\\") {
		return value
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
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
	if ioa.RegisterTools && a.Commands != nil {
		deps := &commands.Deps{
			NodeName: ioa.NodeName,
			NodeMeta: ioa.NodeMeta,
		}
		commands.Provide(deps, ioatools.ClientKey, protocols.ClientAPI(client))
		commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"ioa"}}), deps, a.Commands)
	}
	if ioa.AutoRegister {
		if err := client.EnsureRegistered(ctx, ioa.NodeName, "", ioa.NodeMeta); err != nil {
			a.Logger().Warnf("ioa registration pending: %s", err)
			go a.retryIOARegistration(ctx, client, ioa)
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

func CollectDeepBrowserArtifacts(ctx context.Context, reg *commands.CommandRegistry, targetURL string, logger telemetry.Logger) (string, error) {
	if reg == nil || !reg.Has("playwright") {
		return "", fmt.Errorf("playwright command unavailable; rebuild web with browser tag")
	}
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", fmt.Errorf("target URL is empty")
	}

	session := fmt.Sprintf("deep%d", time.Now().UnixNano())
	closed := false
	defer func() {
		if closed {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = executeRegistryCommand(closeCtx, reg, "playwright close "+session, 5*time.Second)
	}()

	script := `(()=>JSON.stringify({url:location.href,title:document.title,forms:[...document.forms].map((f,i)=>({i,action:f.action,method:f.method,inputs:[...f.elements].map(e=>({tag:e.tagName,type:e.type,name:e.name,id:e.id,placeholder:e.placeholder}))})),buttons:[...document.querySelectorAll("button,input[type=button],input[type=submit],a")].slice(0,80).map(e=>({tag:e.tagName,text:(e.innerText||e.value||e.getAttribute("aria-label")||"").trim(),href:e.href||"",type:e.type||"",id:e.id||"",name:e.name||""})),scripts:[...document.scripts].map(s=>s.src).filter(Boolean).slice(0,50),localStorage:Object.keys(localStorage),sessionStorage:Object.keys(sessionStorage)}))()`
	steps := []struct {
		name    string
		command string
	}{
		{"open", fmt.Sprintf("playwright open %s --session %s --op-timeout 8 --record", quoteCommandArg(targetURL), session)},
		{"network-start", "playwright network " + session + " --start"},
		{"reload", "playwright reload " + session},
		{"wait-idle", "playwright wait-for " + session + " --idle"},
		{"url", "playwright url " + session},
		{"discover", "playwright discover " + session},
		{"text-content", "playwright text-content " + session},
		{"storage-links-scripts", fmt.Sprintf("playwright evaluate %s %s", session, quoteCommandArg(script))},
		{"network-dump", "playwright network " + session + " --dump"},
	}

	const stepTimeout = 12 * time.Second
	var sb strings.Builder
	sb.WriteString("Target: ")
	sb.WriteString(targetURL)
	sb.WriteString("\nSession: ")
	sb.WriteString(session)
	sb.WriteString("\n")
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			appendDeepBrowserStep(&sb, step.name, step.command, "", err)
			break
		}
		out, err := executeRegistryCommand(ctx, reg, step.command, stepTimeout)
		appendDeepBrowserStep(&sb, step.name, step.command, out, err)
		if err != nil && logger != nil {
			logger.Debugf("deep browser step=%s error=%q", step.name, err)
		}
		if err != nil {
			break
		}
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	out, err := executeRegistryCommand(closeCtx, reg, "playwright close "+session, 8*time.Second)
	cancel()
	closed = true
	appendDeepBrowserStep(&sb, "close", "playwright close "+session, out, err)

	artifact := sb.String()
	if tr := truncate.Head(artifact, truncate.Options{}); tr.Truncated {
		artifact = tr.Content + fmt.Sprintf(
			"\n\n[deep browser truncated: showing %d/%d lines (%s of %s)]",
			tr.OutputLines, tr.TotalLines, truncate.FormatSize(tr.OutputBytes), truncate.FormatSize(tr.TotalBytes))
	}
	return artifact, nil
}
