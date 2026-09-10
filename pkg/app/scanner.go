package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/truncate"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func (a *App) initScanner(ctx context.Context, rc Config, logger telemetry.Logger) {
	engineSet := initEngines(ctx, rc.Scanner, logger)
	a.Engines = engineSet

	var options []scan.Option
	provider, providerConfig := a.ProviderState()
	if rc.Scanner.AIEnabled && provider != nil {
		parent := agent.NewAgent(agent.Config{
			Provider:      provider,
			Tools:         a.Commands,
			Model:         providerConfig.Model,
			MaxTokens:     providerConfig.MaxTokens,
			ContextWindow: providerConfig.ContextWindow,
			Logger:        logger,
			Bus:           a,
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

// ScannerState reads readiness at its owner, without exposing the channel.
func (a *App) ScannerState() string {
	if a == nil {
		return "unavailable"
	}
	if !a.enginesEnabled {
		return "disabled"
	}
	if a.enginesReady != nil {
		select {
		case <-a.enginesReady:
			if a.Engines == nil {
				return "failed"
			}
		default:
			return "loading"
		}
	}
	if a.Commands != nil && len(a.Commands.GroupNames("scanner")) > 0 {
		return "ready"
	}
	return "degraded"
}
