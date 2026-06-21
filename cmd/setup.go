package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/cmd/ioaserve"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/pidlock"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/skills"
	ioaclient "github.com/chainreactors/ioa/client"
)

func init() {
	runner.ScannerInitFunc = scannerInit
	runner.ScannerWithAgentFunc = scannerWithAgent
	runner.IOAServeFunc = ioaServe
	runner.IOAClientCommandFunc = ioaClientCommand
}

func scannerInit(ctx context.Context, a *runner.App, rc cfg.RuntimeConfig, logger telemetry.Logger) {
	es := initEngines(ctx, rc.Scanner, logger)
	a.Engines = es
	registerScannerCommands(a.Commands, es, rc.Scanner, rc.Tools, a.Provider, a.ProviderConfig.Model, a.Skills, logger)
}

func initEngines(ctx context.Context, sc cfg.ScannerConfig, logger telemetry.Logger) *engine.Set {
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: sc.CyberhubURL,
		APIKey:      sc.CyberhubKey,
		Mode:        sc.CyberhubMode,
		Proxy:       sc.Proxy,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	engineSet.SetupUncover(engine.ReconOptions{
		FofaEmail:    sc.FofaEmail,
		FofaKey:      sc.FofaKey,
		HunterToken:  sc.HunterToken,
		HunterAPIKey: sc.HunterAPIKey,
		IngressProxy: sc.ReconProxy,
		Limit:        sc.ReconLimit,
	}, logger)
	return engineSet
}

func registerScannerCommands(cmdReg *commands.CommandRegistry, engineSet *engine.Set, scanCfg cfg.ScannerConfig, toolCfg cfg.ToolConfig, llmProvider agent.Provider, model string, skillStore *skills.Store, logger telemetry.Logger) {
	var scanOpts []any
	if scanCfg.AIEnabled && llmProvider != nil {
		scanOpts = append(scanOpts, scan.WithParent(agent.NewAgent(agent.Config{
			Provider: llmProvider,
			Tools:    cmdReg,
			Model:    model,
			Logger:   logger,
		})))
		scanOpts = append(scanOpts, scan.WithDeepBrowserFunc(func(ctx context.Context, targetURL string) (string, error) {
			return runner.CollectDeepBrowserArtifacts(ctx, cmdReg, targetURL, logger)
		}))
		if skillStore != nil {
			scanOpts = append(scanOpts, scan.WithSkillReader(func(name string) string {
				content, ok, err := skillStore.ReadVirtual("aiscan://skills/scan/" + name + ".md")
				if !ok || err != nil {
					return ""
				}
				return content
			}))
		}
	}
	scanOpts = append(scanOpts, scan.WithLogger(logger))

	workDir, _ := os.Getwd()
	deps := &commands.Deps{
		WorkDir:      workDir,
		BashTimeout:  toolCfg.BashTimeout,
		SkillStore:   skillStore,
		EngineSet:    engineSet,
		ScannerProxy: scanCfg.Proxy,
		ScanOpts:     scanOpts,
		Logger:       logger,
		Model:        model,
		TavilyKeys:   toolCfg.TavilyKeys,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
	}
	commands.BuildGroup("scanner", deps, cmdReg)
	commands.BuildGroup("proxy", deps, cmdReg)
	commands.BuildGroup("ioa", deps, cmdReg)
	logger.Infof("scanner commands ready: %v", cmdReg.GroupNames("scanner"))
}

func scannerWithAgent(ctx context.Context, option *cfg.Option, application *runner.App, scannerArgs []string, logger telemetry.Logger) error {
	if application.Provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}

	pidLock, err := pidlock.Acquire(pidlock.AgentPIDFilePath(), logger)
	if err != nil {
		return err
	}
	defer pidLock.Release()

	command := scannerArgs[0]
	intent, err := resolveScannerIntent(option, application.Skills, command)
	if err != nil {
		return err
	}

	rt, err := runner.NewAgentRuntime(ctx, option, logger, &runner.RuntimeConfig{
		ExistingApp: application,
		PromptConfig: &runner.PromptConfig{
			Tools:            application.Commands,
			ScannerDocs:      application.Commands.UsageDocs(),
			Skills:           application.Skills.Skills,
			ScannerAgentMode: true,
			ScannerName:      command,
		},
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	prompt := scan.FormatAgentTaskPrompt(scannerArgs, intent)
	rt.Output.Start("scanner", strings.Join(scannerArgs, " "))

	result, err := agent.NewAgent(rt.Config.
		WithSystemPrompt(rt.SystemPrompt).
		WithStream(false)).
		Run(ctx, prompt)
	if err != nil {
		return err
	}
	if result != nil && strings.TrimSpace(result.Output) != "" {
		rt.Output.Final(result.Output)
	}
	return nil
}

func resolveScannerIntent(option *cfg.Option, store *skills.Store, command string) (string, error) {
	var sections []string
	skillName := scan.ScannerSkillName(command)
	if skillName != "" && cfg.ScannerCommandAvailable(command) {
		if skill, ok := store.ByName(skillName); ok {
			sections = append(sections, store.FormatInvocation(skill, ""))
		}
	}

	intent := strings.TrimSpace(option.Prompt)
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
	intent, err := cfg.ApplySelectedSkills(intent, scan.FilterAutoSkill(option.Skills, command), store)
	if err != nil {
		return "", err
	}
	sections = append(sections, intent)
	return strings.Join(sections, "\n\n"), nil
}

func ioaServe(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	return ioaserve.RunServe(ctx, ioaserve.Config{
		URL:   option.IOAURL,
		Token: option.IOAToken,
		DB:    "",
	}, logger)
}

func ioaClientCommand(ctx context.Context, mode cfg.RunMode, option *cfg.Option, args cfg.IOAClientArgs, logger telemetry.Logger) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	client, err := ioaclient.NewClient(ioaURL, "")
	if err != nil {
		return fmt.Errorf("connect to IOA server: %w", err)
	}
	if client.AccessKey() != "" {
		if err := client.EnsureRegistered(ctx, "aiscan-cli", "", nil); err != nil {
			return fmt.Errorf("IOA auth register: %w", err)
		}
	}

	switch mode {
	case cfg.RunModeIOASpaces:
		return tui.RunIOASpaces(ctx, client, option, os.Stdout, os.Stderr)
	case cfg.RunModeIOAMessages:
		return tui.RunIOAMessages(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOAContext:
		return tui.RunIOAContext(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOANodes:
		return tui.RunIOANodes(ctx, client, option, args, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown ioa mode: %s", mode)
	}
}
