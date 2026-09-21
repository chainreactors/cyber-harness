package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/truncate"

	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/scan/engine"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

func executeRegistryCommand(ctx context.Context, registry coretool.CommandExecutor, bash *terminaltool.BashTool, commandLine string, timeout time.Duration) (string, error) {
	if registry == nil || bash == nil {
		return "", fmt.Errorf("bash tool is not registered")
	}
	var output strings.Builder
	execution, err := bash.RunForeground(ctx, commandLine, terminaltool.BashExecOptions{
		Timeout: timeout,
		OnOutput: func(data []byte) {
			_, _ = output.Write(data)
		},
	})
	if err != nil {
		return output.String(), err
	}
	info, retained := execution.Session()
	if !retained && execution.ID != "" {
		return output.String(), fmt.Errorf("command session %s is no longer available", execution.ID)
	}
	if info.ExitStatus() != 0 {
		return output.String(), fmt.Errorf("command exited with code %d", info.ExitStatus())
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

func collectDeepBrowserArtifacts(ctx context.Context, registry coretool.CommandExecutor, bash *terminaltool.BashTool, targetURL string, logger telemetry.Logger) (string, error) {
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
		_, _ = executeRegistryCommand(closeCtx, registry, bash, "playwright close "+session, 5*time.Second)
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
		content, err := executeRegistryCommand(ctx, registry, bash, step.command, 12*time.Second)
		appendDeepBrowserStep(&output, step.name, step.command, content, err)
		if err != nil {
			if logger != nil {
				logger.Debugf("deep browser step=%s error=%q", step.name, err)
			}
			break
		}
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	content, err := executeRegistryCommand(closeCtx, registry, bash, "playwright close "+session, 8*time.Second)
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

func newScanCommand(engines *engine.Set, options []scan.Option, proxy string, events aop.EventPublisher) (coretool.Command, error) {
	if engines == nil || engines.Gogo == nil || engines.Spray == nil {
		return coretool.Command{}, fmt.Errorf("scan engines are unavailable")
	}
	scanOptions := append([]scan.Option(nil), options...)
	if proxy != "" {
		scanOptions = append(scanOptions, scan.WithProxy(proxy))
	}
	if events != nil {
		scanOptions = append(scanOptions, scan.WithEvents(events))
	}
	impl := scan.New(engines, scanOptions...)
	return coretool.Command{
		Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
		DescriptionPath: "cyber://skills/cyber/okf/easm/scan.md",
		Run:             impl.Run,
	}, nil
}
