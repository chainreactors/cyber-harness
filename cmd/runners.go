package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	acpclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/loop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
)

func runAgentMode(ctx context.Context, option *Option, logger telemetry.Logger) error {
	if option.Loop {
		return runLoop(ctx, option, logger)
	}
	if !hasAgentOneShotInput(option) {
		return runInteractiveAgentMode(ctx, option, logger)
	}
	return runAgentOneShotMode(ctx, option, logger)
}

type agentRuntime struct {
	application  *app.App
	systemPrompt string
}

func newAgentRuntime(ctx context.Context, option *Option, logger telemetry.Logger) (*agentRuntime, error) {
	application, err := app.New(ctx, appConfig(option, runtimeFeatures{
		ProviderEnabled:     true,
		ToolsEnabled:        true,
		VerificationEnabled: true,
		VerifyMinPriority:   "high",
	}, logger))
	if err != nil {
		return nil, fmt.Errorf("init app: %w", err)
	}
	applyResolvedProviderOptions(option, application.ProviderConfig)

	for _, diagnostic := range application.SkillDiagnostics {
		logger.Warnf("skill %s: %s", diagnostic.Path, diagnostic.Message)
	}

	if err := registerACPTools(ctx, application, option); err != nil {
		application.Close()
		return nil, fmt.Errorf("init acp tools: %w", err)
	}

	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:       application.Commands,
		ScannerDocs: application.Commands.UsageDocs(),
		Skills:      application.Skills.Skills,
	})
	logger.Debugf("system prompt length: %d chars", len(systemPrompt))
	return &agentRuntime{application: application, systemPrompt: systemPrompt}, nil
}

func runAgentOneShotMode(ctx context.Context, option *Option, logger telemetry.Logger) error {
	task, err := resolveTask(option)
	if err != nil {
		return err
	}
	displayTask := task

	runtime, err := newAgentRuntime(ctx, option, logger)
	if err != nil {
		return err
	}
	defer runtime.application.Close()

	application := runtime.application
	task = skills.ExpandCommand(task, application.Skills)
	task, err = applySelectedSkills(task, option.Skills, application.Skills)
	if err != nil {
		return err
	}

	output := newAgentOutput(option)
	output.Start("task", displayTask)

	result, err := agent.RunWithEvents(ctx, task, application.Commands, output.HandleEvent,
		agent.WithProvider(application.Provider),
		agent.WithSystemPrompt(runtime.systemPrompt),
		agent.WithModel(option.Model),
		agent.WithStream(false),
		agent.WithLogger(telemetry.NopLogger()),
	)
	if err != nil {
		return err
	}
	if result != nil && strings.TrimSpace(result.Output) != "" {
		output.Final(result.Output)
	}
	return nil
}

func runDirectScannerMode(ctx context.Context, option *Option, rest []string, logger telemetry.Logger) error {
	features, scannerArgs, err := directScannerRuntimeFeatures(rest)
	if err != nil {
		return err
	}
	if option.AI {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.ToolsEnabled = true
	}
	if isScannerHelpRequest(scannerArgs) {
		if usage, ok := staticScannerUsage(scannerArgs[0]); ok {
			fmt.Print(usage)
			if !strings.HasSuffix(usage, "\n") {
				fmt.Println()
			}
			return nil
		}
	}
	application, err := app.New(ctx, appConfig(option, features, logger))
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer application.Close()
	applyResolvedProviderOptions(option, application.ProviderConfig)

	if !application.Commands.Has(scannerArgs[0]) {
		return fmt.Errorf("unknown subcommand: %s", scannerArgs[0])
	}
	if option.AI && scannerArgs[0] != "scan" {
		return runScannerAgentMode(ctx, option, application, scannerArgs, logger)
	}
	var stream io.Writer
	if option.NoColor && scannerArgs[0] == "scan" && !hasScannerFlag(scannerArgs[1:], "--no-color") {
		scannerArgs = append(scannerArgs, "--no-color")
	}
	if shouldStreamScannerOutput(scannerArgs) {
		stream = os.Stdout
	}
	out, err := application.Commands.ExecuteArgsStreaming(ctx, scannerArgs, stream)
	if err != nil {
		return err
	}
	fmt.Print(out)
	if option.AI {
		output := newAgentOutput(option)
		output.Start("analysis", strings.Join(scannerArgs, " "))
		result, err := runScannerAIProcess(ctx, option, application, scannerArgs, out, logger)
		if err != nil {
			return err
		}
		if strings.TrimSpace(result) != "" {
			output.Final(result)
		}
	}
	return nil
}

func runLoop(ctx context.Context, option *Option, logger telemetry.Logger) error {
	if option.Heartbeat < 0 {
		return fmt.Errorf("--heartbeat must be greater than or equal to 0")
	}
	acpURL := option.ACPURL
	if acpURL == "" {
		acpURL = "http://127.0.0.1:8765"
	}
	cfg := appConfig(option, runtimeFeatures{
		ProviderEnabled:     true,
		ToolsEnabled:        true,
		VerificationEnabled: true,
		VerifyMinPriority:   "high",
	}, logger)
	cfg.ACP = &app.ACPConfig{
		URL:           acpURL,
		NodeID:        option.ACPNodeID,
		NodeName:      option.ACPNodeName,
		RegisterTools: true,
		AutoRegister:  false,
	}
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer application.Close()
	applyResolvedProviderOptions(option, application.ProviderConfig)
	for _, diagnostic := range application.SkillDiagnostics {
		logger.Warnf("skill %s: %s", diagnostic.Path, diagnostic.Message)
	}

	intent := strings.TrimSpace(option.Prompt)
	if intent != "" && len(option.Inputs) > 0 {
		intent = fmt.Sprintf("%s\n\nTargets:\n%s", intent, formatInputs(option.Inputs))
	}
	rawPrompt := intent
	intent, err = applySelectedSkills(intent, option.Skills, application.Skills)
	if err != nil {
		return err
	}

	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:       application.Commands,
		ScannerDocs: application.Commands.UsageDocs(),
		Skills:      application.Skills.Skills,
	})

	streamClient, ok := application.ACPStreamClient.(acpclient.StreamAPI)
	if !ok || streamClient == nil {
		return fmt.Errorf("loop requires streaming ACP client")
	}
	runner := loop.New(loop.Config{
		Client:            streamClient,
		Provider:          application.Provider,
		Tools:             application.Commands,
		SystemPrompt:      systemPrompt,
		Model:             option.Model,
		Stream:            true,
		NodeName:          defaultACPNodeName(option),
		SpaceName:         option.Space,
		SpaceDescription:  "aiscan loop worker",
		PollInterval:      2 * time.Second,
		HeartbeatInterval: time.Duration(option.Heartbeat) * time.Minute,
		Prompt:            rawPrompt,
		Intent:            intent,
		Skills:            option.Skills,
		Logger:            logger,
	})
	return runner.Run(ctx)
}

func runACPServe(ctx context.Context, option *Option, logger telemetry.Logger) error {
	return ioaserver.RunServer(ctx, ioaserver.ServerOptions{
		URL:    option.ACPURL,
		DB:     option.ACPDB,
		Logger: logger,
	})
}

func registerACPTools(ctx context.Context, application *app.App, option *Option) error {
	acpURL := option.ACPURL
	if acpURL == "" {
		return nil
	}
	cfg := app.ACPConfig{
		URL:           acpURL,
		NodeID:        option.ACPNodeID,
		NodeName:      option.ACPNodeName,
		RegisterTools: true,
		AutoRegister:  true,
		NodeMeta:      map[string]any{"client": "aiscan"},
	}
	if cfg.NodeName == "" {
		cfg.NodeName = defaultACPNodeName(option)
	}
	return application.InitACP(ctx, cfg)
}
