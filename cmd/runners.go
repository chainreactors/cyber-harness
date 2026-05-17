package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/swarm"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/aiscan/skills"
	ioaserver "github.com/chainreactors/ioa/server"
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
		ProviderEnabled: true,
		ToolsEnabled:    true,
		AIEnabled:       true,
	}, logger))
	if err != nil {
		return nil, fmt.Errorf("init app: %w", err)
	}
	applyResolvedProviderOptions(option, application.ProviderConfig)

	for _, diagnostic := range application.SkillDiagnostics {
		logger.Warnf("skill %s: %s", diagnostic.Path, diagnostic.Message)
	}

	if err := registerIOATools(ctx, application, option); err != nil {
		application.Close()
		return nil, fmt.Errorf("init ioa tools: %w", err)
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
	if features.Warning != "" && !option.Quiet {
		fmt.Fprintf(os.Stderr, "warning: %s\n", features.Warning)
	}
	scanAI := option.AI || features.ScannerAI
	if scanAI && len(scannerArgs) > 0 && scannerArgs[0] == "scan" {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.ToolsEnabled = true
		features.AIEnabled = true
	}
	if option.AI {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.ToolsEnabled = true
		features.AIEnabled = true
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
	scannerLogger := logger
	if !directScannerDebugEnabled(option, scannerArgs) {
		scannerLogger = telemetry.ErrorOnlyLogger(logger)
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
	}
	application, err := app.New(ctx, appConfig(option, features, scannerLogger))
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer application.Close()
	applyResolvedProviderOptions(option, application.ProviderConfig)

	if !application.Commands.Has(scannerArgs[0]) {
		return fmt.Errorf("unknown subcommand: %s", scannerArgs[0])
	}
	if option.Debug && scannerCommandSupportsDebug(scannerArgs[0]) && !toolargs.BoolFlagEnabled(scannerArgs[1:], "--debug") {
		scannerArgs = append(scannerArgs, "--debug")
	}
	if option.AI && scannerArgs[0] != "scan" {
		return runScannerAgentMode(ctx, option, application, scannerArgs, logger)
	}
	var stream io.Writer
	var streamCapture bytes.Buffer
	if option.NoColor && scannerArgs[0] == "scan" && !hasScannerFlag(scannerArgs[1:], "--no-color") {
		scannerArgs = append(scannerArgs, "--no-color")
	}
	if shouldStreamScannerOutput(scannerArgs) {
		if scanAI {
			stream = io.MultiWriter(os.Stdout, &streamCapture)
		} else {
			stream = os.Stdout
		}
	}
	out, err := application.Commands.ExecuteArgsStreaming(ctx, scannerArgs, stream)
	if err != nil {
		return err
	}
	fmt.Print(out)
	if scanAI {
		aiInput := out
		if streamCapture.Len() > 0 {
			aiInput = streamCapture.String() + out
		}
		output := newAgentOutput(option)
		output.Start("analysis", strings.Join(scannerArgs, " "))
		result, err := runScannerAIProcess(ctx, option, application, scannerArgs, aiInput, logger)
		if err != nil {
			return err
		}
		if strings.TrimSpace(result) != "" {
			output.Final(result)
		}
	}
	return nil
}

func directScannerDebugEnabled(option *Option, scannerArgs []string) bool {
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

func runLoop(ctx context.Context, option *Option, logger telemetry.Logger) error {
	if option.Heartbeat < 0 {
		return fmt.Errorf("--heartbeat must be greater than or equal to 0")
	}
	ioaURL := option.IOAURL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	cfg := appConfig(option, runtimeFeatures{
		ProviderEnabled: true,
		ToolsEnabled:    true,
		AIEnabled:       true,
	}, logger)
	cfg.IOA = &app.IOAConfig{
		URL:           ioaURL,
		NodeID:        option.IOANodeID,
		NodeName:      option.IOANodeName,
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

	streamClient := application.IOAStreamClient
	if streamClient == nil {
		return fmt.Errorf("loop requires streaming IOA client")
	}

	taskHandler := func(ctx context.Context, task swarm.Task) (string, error) {
		return agent.Run(ctx, task.Content, application.Commands,
			agent.WithProvider(application.Provider),
			agent.WithSystemPrompt(systemPrompt),
			agent.WithModel(option.Model),
			agent.WithStream(true),
			agent.WithLogger(logger),
		)
	}

	heartbeatFunc := func(ctx context.Context, prompt string) (string, error) {
		return agent.Run(ctx, prompt, application.Commands,
			agent.WithProvider(application.Provider),
			agent.WithSystemPrompt(systemPrompt),
			agent.WithModel(option.Model),
			agent.WithStream(true),
			agent.WithLogger(logger),
		)
	}

	node := swarm.NewNode(swarm.NodeConfig{
		Client:                streamClient,
		NodeName:              defaultIOANodeName(option),
		SpaceName:             option.Space,
		SpaceDescription:      "aiscan loop worker",
		PollInterval:          2 * time.Second,
		HeartbeatInterval:     time.Duration(option.Heartbeat) * time.Minute,
		HeartbeatContextLimit: 50,
		Prompt:                rawPrompt,
		Intent:                intent,
		Skills:                option.Skills,
		OnTask:                taskHandler,
		OnHeartbeat:           heartbeatFunc,
		Logger:                logger,
	})

	application.Commands.RegisterTool(swarm.CronCommand(node))

	return node.Run(ctx)
}

func runIOAServe(ctx context.Context, option *Option, logger telemetry.Logger) error {
	return ioaserver.RunServer(ctx, ioaserver.ServerOptions{
		URL:    option.IOAURL,
		DB:     option.IOADB,
		Logger: logger,
	})
}

func registerIOATools(ctx context.Context, application *app.App, option *Option) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		return nil
	}
	cfg := app.IOAConfig{
		URL:           ioaURL,
		NodeID:        option.IOANodeID,
		NodeName:      option.IOANodeName,
		RegisterTools: true,
		AutoRegister:  true,
		NodeMeta:      map[string]any{"client": "aiscan"},
	}
	if cfg.NodeName == "" {
		cfg.NodeName = defaultIOANodeName(option)
	}
	return application.InitIOA(ctx, cfg)
}
