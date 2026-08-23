package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/capability"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/pidlock"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/skills"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func DirectScannerRuntimeFeatures(rest []string) (RuntimeFeatures, []string, error) {
	return DirectScannerRuntimeFeaturesWithDefault(rest, cfg.DefaultVerify)
}

func DirectScannerRuntimeFeaturesWithDefault(rest []string, defaultVerify string) (RuntimeFeatures, []string, error) {
	if len(rest) == 0 {
		return RuntimeFeatures{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return RuntimeFeatures{}, rest, nil
	}
	verifyMode, explicit := scannerVerifyMode(rest[1:], defaultVerify)
	sniperEnabled := HasScannerFlag(rest[1:], "--sniper")
	deepEnabled := HasScannerFlag(rest[1:], "--deep")
	aiSkillRequested := sniperEnabled || deepEnabled

	features := RuntimeFeatures{}

	if aiSkillRequested {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.AIEnabled = true
		features.ScannerAI = true
	}

	switch verifyMode {
	case "auto":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = true
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, removeScannerFlag(rest, "--verify"), nil
	case "off":
		if explicit {
			return features, replaceOrAppendScannerFlag(rest, "--verify", "off"), nil
		}
		return features, rest, nil
	case "low", "medium", "high", "critical":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = !explicit
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, rest, nil
	default:
		if explicit {
			return RuntimeFeatures{}, nil, fmt.Errorf("invalid --verify value %q: expected auto, off, low, medium, high, or critical", verifyMode)
		}
		return features, rest, nil
	}
}

func HasScannerFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func ShouldStreamScannerOutput(rest []string) bool {
	if len(rest) == 0 || rest[0] != "scan" {
		return false
	}
	if isDirectScannerJSONOutput(rest) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "--report" {
			return false
		}
		if strings.HasPrefix(arg, "--report=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--report=")))
			if value != "false" && value != "0" && value != "no" {
				return false
			}
		}
	}
	return true
}

func isDirectScannerJSONOutput(rest []string) bool {
	if len(rest) == 0 || !cfg.ScannerCommandAvailable(rest[0]) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "-j" || arg == "--json" {
			return true
		}
		if strings.HasPrefix(arg, "--json=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--json=")))
			return value != "false" && value != "0" && value != "no"
		}
	}
	return false
}

func scannerVerifyMode(args []string, defaultVerify string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(arg, "=")
		if key != "--verify" {
			continue
		}
		if hasValue {
			return strings.ToLower(strings.TrimSpace(value)), true
		}
		if i+1 < len(args) {
			return strings.ToLower(strings.TrimSpace(args[i+1])), true
		}
		return "", true
	}
	return defaultVerifyMode(defaultVerify), false
}

func replaceOrAppendScannerFlag(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		arg := out[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			continue
		}
		if hasValue {
			out[i] = flag + "=" + value
			return out
		}
		if i+1 < len(out) {
			out[i+1] = value
			return out
		}
		out = append(out, value)
		return out
	}
	return append(out, flag+"="+value)
}

func defaultVerifyMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "off"
	}
	return value
}

func removeScannerFlag(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			out = append(out, arg)
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
		}
	}
	return out
}

func (a *App) initScanner(ctx context.Context, rc ApplicationConfig, logger telemetry.Logger) {
	engineSet := initEngines(ctx, rc.Scanner, logger)
	a.Engines = engineSet

	var options []scan.Option
	if rc.Scanner.AIEnabled && a.Provider != nil {
		parent := agent.NewAgent(agent.Config{
			Provider:      a.Provider,
			Tools:         a.Commands,
			Model:         a.ProviderConfig.Model,
			MaxTokens:     a.ProviderConfig.MaxTokens,
			ContextWindow: a.ProviderConfig.ContextWindow,
			Logger:        logger,
			Bus:           a.Events,
		})
		options = append(options,
			scan.WithParent(parent),
			scan.WithDeepBrowserFunc(func(ctx context.Context, targetURL string) (string, error) {
				return CollectDeepBrowserArtifacts(ctx, a.Commands, targetURL, logger)
			}),
		)
		if a.Skills != nil {
			options = append(options, scan.WithSkillReader(func(name string) string {
				content, ok, err := a.Skills.ReadVirtual("aiscan://skills/scan/" + name + ".md")
				if !ok || err != nil {
					return ""
				}
				return content
			}))
		}
	}
	options = append(options, scan.WithLogger(logger))

	a.assemblyMu.Lock()
	defer a.assemblyMu.Unlock()
	commands.Provide(a.deps, scan.OptsKey, options)
	if engineSet != nil {
		commands.Provide(a.deps, engine.SetKey, engineSet)
		commands.Provide(a.deps, resources.SetKey, engineSet.Resources)
	}
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"scanner"}}), a.deps, a.Commands)
	logger.Infof("%s", telemetry.StartupOK("scanner", strings.Join(a.Commands.GroupNames("scanner"), ",")))
}

func initEngines(ctx context.Context, scanner ScannerConfig, logger telemetry.Logger) *engine.Set {
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: scanner.CyberhubURL,
		APIKey:      scanner.CyberhubKey,
		Mode:        scanner.CyberhubMode,
		Proxy:       scanner.Proxy,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	engineSet.SetupUncover(engine.ReconOptions{
		FofaKey:      scanner.FofaKey,
		HunterAPIKey: scanner.HunterAPIKey,
		IngressProxy: scanner.ReconProxy,
		Limit:        scanner.ReconLimit,
		Credentials:  scanner.UncoverCredentials,
	}, logger)
	return engineSet
}

func runScannerWithAgent(ctx context.Context, option *cfg.Option, application *App, scannerArgs []string, logger telemetry.Logger) error {
	if application.Provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}
	lock, err := pidlock.Acquire(pidlock.AgentPIDFilePath(), logger)
	if err != nil {
		return err
	}
	defer lock.Release()

	intent, err := resolveScannerIntent(option, application.Skills, scannerArgs[0])
	if err != nil {
		return err
	}
	runtime, err := NewAgentRuntime(ctx, option, logger, &RuntimeConfig{
		ExistingApp: application,
		PromptConfig: &PromptConfig{
			Tools:            application.Commands,
			ScannerDocs:      application.Commands.UsageDocs(),
			Skills:           application.Skills.Skills,
			ScannerAgentMode: true,
			ScannerName:      scannerArgs[0],
		},
	})
	if err != nil {
		return err
	}
	defer runtime.Close()

	prompt := scan.FormatAgentTaskPrompt(scannerArgs, intent)
	output := tui.NewStaticAgentOutput(option)
	unsubscribe := runtime.Subscribe(output.HandleEvent)
	defer unsubscribe()
	output.Start("scanner", strings.Join(scannerArgs, " "))
	session, err := runtime.OpenSession(ctx, SessionOptions{ID: "scanner"})
	if err != nil {
		return err
	}
	run, err := session.Run(ctx, RunInput{Content: []*aop.Content{aop.Text(prompt)}})
	if err != nil {
		return err
	}
	result, err := run.Wait()
	if strings.TrimSpace(result.Output) != "" {
		output.Final(result.Output)
	}
	_ = runtime.CloseSession(context.Background(), "scanner", SessionCloseCompleted)
	return err
}

func resolveScannerIntent(option *cfg.Option, store *skills.Store, command string) (string, error) {
	var sections []string
	if conceptURI := scan.ScannerConceptURI(command); conceptURI != "" && cfg.ScannerCommandAvailable(command) {
		if body, ok, err := store.ReadVirtualBody(conceptURI); err == nil && ok && body != "" {
			sections = append(sections, skills.FormatVirtualInvocation(command, conceptURI, body))
		}
	}
	intent, err := cfg.ResolvePrompt(option.Prompt)
	if err != nil {
		return "", err
	}
	if intent == "" && option.TaskFile != "" {
		data, err := os.ReadFile(option.TaskFile)
		if err != nil {
			return "", fmt.Errorf("read task file: %w", err)
		}
		intent = strings.TrimSpace(string(data))
	}
	if intent == "" {
		intent = "Process the scanner output according to the user's intent. If no specific intent is provided, briefly explain the important evidence in the output."
	}
	intent, err = cfg.ApplySelectedSkills(intent, scan.FilterAutoSkill(option.Skills, command), store)
	if err != nil {
		return "", err
	}
	return strings.Join(append(sections, intent), "\n\n"), nil
}

func executeRegistryCommand(ctx context.Context, registry *commands.CommandRegistry, commandLine string, timeout time.Duration) (string, error) {
	tool, ok := registry.GetTool("bash")
	if !ok {
		return "", fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*commands.BashTool)
	if !ok {
		return "", fmt.Errorf("registered bash tool has unexpected type")
	}
	var output strings.Builder
	execution, err := bash.RunForeground(ctx, commandLine, commands.BashExecOptions{
		Timeout: timeout,
		OnOutput: func(data []byte) {
			_, _ = output.Write(data)
		},
	})
	if err != nil {
		return output.String(), err
	}
	if execution.ExitCode != 0 {
		return output.String(), fmt.Errorf("command exited with code %d", execution.ExitCode)
	}
	return output.String(), nil
}

func appendDeepBrowserStep(output *strings.Builder, name, commandLine, content string, err error) {
	output.WriteString("\n## ")
	output.WriteString(name)
	output.WriteString("\nCommand: `")
	output.WriteString(commandLine)
	output.WriteString("`\n")
	if err != nil {
		output.WriteString("Error: ")
		output.WriteString(err.Error())
		output.WriteString("\n")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	truncated := truncate.Head(content, truncate.Options{})
	output.WriteString(truncated.Content)
	if truncated.Truncated {
		output.WriteString(fmt.Sprintf("\n[step truncated: %d/%d lines]", truncated.OutputLines, truncated.TotalLines))
	}
	output.WriteString("\n")
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

func CollectDeepBrowserArtifacts(ctx context.Context, registry *commands.CommandRegistry, targetURL string, logger telemetry.Logger) (string, error) {
	if registry == nil || !registry.Has("playwright") {
		return "", fmt.Errorf("playwright command unavailable")
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
		_, _ = executeRegistryCommand(closeCtx, registry, "playwright close "+session, 5*time.Second)
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
		{"inner-text", "playwright inner-text " + session + " body"},
		{"storage-links-scripts", fmt.Sprintf("playwright evaluate %s %s", session, quoteCommandArg(script))},
		{"network-dump", "playwright network " + session + " --dump"},
	}

	var output strings.Builder
	output.WriteString("Target: " + targetURL + "\nSession: " + session + "\n")
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			appendDeepBrowserStep(&output, step.name, step.command, "", err)
			break
		}
		content, err := executeRegistryCommand(ctx, registry, step.command, 12*time.Second)
		appendDeepBrowserStep(&output, step.name, step.command, content, err)
		if err != nil {
			if logger != nil {
				logger.Debugf("deep browser step=%s error=%q", step.name, err)
			}
			break
		}
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	content, err := executeRegistryCommand(closeCtx, registry, "playwright close "+session, 8*time.Second)
	cancel()
	closed = true
	appendDeepBrowserStep(&output, "close", "playwright close "+session, content, err)

	artifact := truncate.Head(output.String(), truncate.Options{})
	if !artifact.Truncated {
		return artifact.Content, nil
	}
	return artifact.Content + fmt.Sprintf(
		"\n\n[deep browser truncated: showing %d/%d lines (%s of %s)]",
		artifact.OutputLines, artifact.TotalLines, truncate.FormatSize(artifact.OutputBytes), truncate.FormatSize(artifact.TotalBytes),
	), nil
}
