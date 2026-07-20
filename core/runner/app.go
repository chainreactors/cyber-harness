package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/probe"
	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
	ioaclient "github.com/chainreactors/ioa/client"
)

type App struct {
	Provider          agent.Provider
	ProviderConfig    agent.ProviderConfig
	ProviderFallbacks []agent.ProviderEntry
	Commands          *commands.CommandRegistry
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

func NewApp(ctx context.Context, rc cfg.RuntimeConfig) (*App, error) {
	a := &App{}
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a.logger = logger
	logger = a.Logger()

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

	a.Commands = initCoreCommands(rc, a.Provider, a.Skills, logger)

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
			if closer, ok := cmd.(interface{ Close() }); ok {
				closer.Close()
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

// optionalToolGroups lists all selectable tool groups that can be enabled via
// --tools or config.  Arsenal is always loaded and is NOT in this list.
var optionalToolGroups = []string{"search", "browser"}

func initCoreCommands(rc cfg.RuntimeConfig, llmProvider agent.Provider, skillStore *skills.Store, logger telemetry.Logger) *commands.CommandRegistry {
	cmdReg := commands.NewRegistry()
	workDir, _ := os.Getwd()
	deps := &commands.Deps{
		WorkDir:     workDir,
		BashTimeout: rc.Tools.BashTimeout,
		SkillStore:  skillStore,
		Provider:    llmProvider,
		Logger:      logger,
		TavilyKeys:  rc.Tools.TavilyKeys,
	}
	commands.BuildGroup("core", deps, cmdReg)
	commands.BuildGroup("arsenal", deps, cmdReg)

	enabled := rc.Tools.OptionalTools
	if len(enabled) == 0 {
		for _, g := range optionalToolGroups {
			commands.BuildGroup(g, deps, cmdReg)
		}
	} else {
		for _, g := range enabled {
			commands.BuildGroup(g, deps, cmdReg)
		}
	}
	return cmdReg
}

func executeRegistryCommand(ctx context.Context, reg *commands.CommandRegistry, commandLine string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		return reg.Execute(ctx, commandLine)
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := reg.Execute(stepCtx, commandLine)
		done <- result{out: out, err: err}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-stepCtx.Done():
		return "", fmt.Errorf("command timed out after %s: %w", timeout, stepCtx.Err())
	}
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

func (a *App) InitIOA(ctx context.Context, ioa cfg.IOAConfig) error {
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
			IOAClient: client,
			NodeName:  ioa.NodeName,
			NodeMeta:  ioa.NodeMeta,
		}
		commands.BuildGroup("ioa", deps, a.Commands)
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

func (a *App) retryIOARegistration(ctx context.Context, client *ioaclient.Client, ioa cfg.IOAConfig) {
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

func (a *App) configureIOASpace(ctx context.Context, client *ioaclient.Client, ioa cfg.IOAConfig) {
	if ioa.Space != "" && client != nil && client.Bound() {
		info, err := client.Space(ctx, ioa.Space, "aiscan agent")
		if err == nil {
			a.setIOASpace(info.ID)
		}
	}
}

func (a *App) setIOASpace(spaceID string) {
	for _, cmd := range a.Commands.All() {
		if setter, ok := cmd.(interface{ SetDefaultSpace(string) }); ok {
			setter.SetDefaultSpace(spaceID)
		}
	}
}

func newIOAClient(ioa cfg.IOAConfig) (*ioaclient.Client, error) {
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
		_, _ = reg.Execute(closeCtx, "playwright close "+session)
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
