package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/operation"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	cmdpkg "github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/console"
	"github.com/chainreactors/aiscan/pkg/edition"
	agentext "github.com/chainreactors/aiscan/pkg/exts/session"
	profile "github.com/chainreactors/aiscan/pkg/profile"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	"github.com/chainreactors/aiscan/tools/toolargs"
)

// ---------------------------------------------------------------------------
// Mode dispatch
// ---------------------------------------------------------------------------

func RunAgentMode(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, setInterrupt ...func(func() bool)) error {
	var si func(func() bool)
	if len(setInterrupt) > 0 {
		si = setInterrupt[0]
	}
	if !cfg.HasAgentOneShotInput(option) {
		if option != nil && option.OutputFormat != "" && option.OutputFormat != "text" {
			return fmt.Errorf("--output-format=%s is only available for one-shot agent runs", option.OutputFormat)
		}
		return runInteractiveMode(ctx, factory, option, logger, si)
	}
	return runOneShotMode(ctx, factory, option, logger)
}

// ---------------------------------------------------------------------------
// Agent one-shot
// ---------------------------------------------------------------------------

func runOneShotMode(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger) error {
	task, err := cfg.ResolveTask(option)
	if err != nil {
		return err
	}

	product, rt, err := loadAgentProfile(ctx, factory, option, logger, &agentext.Config{Loop: agent.StandardLoop{}})
	if err != nil {
		return err
	}
	defer product.Close(context.Background())

	task = skills.ExpandCommand(task, rt.App().Skills)
	task, err = cfg.ApplySelectedSkills(task, option.Skills, rt.App().Skills)
	if err != nil {
		return err
	}

	return console.RunTask(ctx, rt, option, "task", "task", task, agentext.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalMaxRounds: option.EvalMaxRetries,
	})
}

// ---------------------------------------------------------------------------
// Agent interactive (REPL)
// ---------------------------------------------------------------------------

func runInteractiveMode(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, setInterrupt func(func() bool)) error {
	product, rt, err := loadAgentProfile(ctx, factory, option, logger, &agentext.Config{
		PrimarySessionID: console.MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		return err
	}
	defer product.Close(context.Background())

	if _, err := cfg.ApplySelectedSkills("", option.Skills, rt.App().Skills); err != nil {
		return err
	}

	if setInterrupt != nil {
		setInterrupt(func() bool { return false })
	}
	return console.AttachLocalREPL(ctx, rt, option, product.ConsoleBindings())
}

// ---------------------------------------------------------------------------
// Scanner direct execution
// ---------------------------------------------------------------------------

func RunDirectScannerMode(ctx context.Context, factory profile.Factory, option *cfg.Option, rest []string, logger telemetry.Logger) (runErr error) {
	defaultVerify := cfg.ResolveString(option.ScanConfig.Verify, cfg.DefaultVerify)
	features, scannerArgs, err := DirectScannerRuntimeFeaturesWithDefault(rest, defaultVerify)
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
		if usage, ok := edition.Catalog().Usage(scannerArgs[0]); ok {
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

	product, err := factory.Build(profile.Request{Option: option, Features: features, Logger: scannerLogger})
	if err != nil {
		return fmt.Errorf("construct scanner profile: %w", err)
	}
	if err := product.Load(ctx); err != nil {
		return fmt.Errorf("load scanner profile: %w", err)
	}
	defer product.Close(context.Background())
	application, err := product.App()
	if err != nil {
		return err
	}
	if err := application.WaitEngines(ctx); err != nil {
		return fmt.Errorf("engine init: %w", err)
	}
	_, providerConfig := application.ProviderState()
	apppkg.ApplyResolvedProviderOptions(option, providerConfig)

	if !application.Commands.Has(scannerArgs[0]) {
		return fmt.Errorf("unknown subcommand: %s", scannerArgs[0])
	}
	if option.Debug && scannerCommandSupportsDebug(scannerArgs[0]) && !toolargs.BoolFlagEnabled(scannerArgs[1:], "--debug") {
		scannerArgs = append(scannerArgs, "--debug")
	}

	if option.AI && scannerArgs[0] != "scan" {
		return runScannerWithAgent(ctx, option, application, scannerArgs, logger)
	}

	if option.NoColor && scannerArgs[0] == "scan" && !HasScannerFlag(scannerArgs[1:], "--no-color") {
		scannerArgs = append(scannerArgs, "--no-color")
	}
	sessionID := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	turnID := sessionID + "-run"
	emitter := scannerArgs[0]
	bash := application.Bash
	if bash == nil {
		return fmt.Errorf("bash tool is not registered")
	}
	callID := turnID + "-call"
	ctx = operation.ContextWithInvocation(ctx, operation.Invocation{
		CallID: callID, SessionID: sessionID, TurnID: turnID, Emitter: emitter,
	})
	arguments, err := aop.JSONValue(map[string]any{"args": scannerArgs[1:]})
	if err != nil {
		return fmt.Errorf("encode scanner arguments: %w", err)
	}
	startedAt := time.Now()
	emitSessionStarted(application, sessionID, emitter, &aop.SessionStarted{}, types.SessionHistory_MODE_INHERIT)
	application.Publish(&aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: emitter,
		Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}},
	})
	application.Publish(&aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: emitter,
		Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{Id: callID, Name: emitter, Arguments: arguments}},
	})
	defer func() {
		isCanceled := errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
		result := &aop.ToolResult{
			CallId: callID, Name: emitter, IsError: runErr != nil,
			DurationMs: uint64(time.Since(startedAt).Milliseconds()),
		}
		stopReason := string(agent.StopReasonCompleted)
		closeReason := agentext.SessionCloseCompleted
		if runErr != nil {
			result.Output = []*aop.Content{aop.Text(runErr.Error())}
			stopReason = string(agent.StopReasonError)
			closeReason = agentext.SessionCloseError
		}
		if isCanceled {
			stopReason = string(agent.StopReasonCanceled)
			closeReason = agentext.SessionCloseCanceled
		}
		application.Publish(&aop.Event{
			SessionId: sessionID, TurnId: turnID, Emitter: emitter,
			Payload: &aop.Event_ToolResult{ToolResult: result},
		})
		application.Publish(&aop.Event{
			SessionId: sessionID, TurnId: turnID, Emitter: emitter,
			Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: stopReason}},
		})
		emitSessionEnded(application, sessionID, emitter, string(closeReason))
	}()
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
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !streaming {
		fmt.Print(captured.String())
	}
	info, retained := execution.Session()
	if !retained && execution.ID != "" {
		return fmt.Errorf("command session %s is no longer available", execution.ID)
	}
	if info.ExitCode != 0 {
		return fmt.Errorf("%s exited with code %d", scannerArgs[0], info.ExitCode)
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
	case "scan", "gogo", "spray", "zombie", "neutron", "proton":
		return true
	default:
		return false
	}
}

func emitSessionStarted(application *apppkg.App, sessionID, agentName string, started *aop.SessionStarted, historyMode types.SessionHistory_Mode) {
	event := &aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionStarted{SessionStarted: started}}
	_ = types.SetSessionHistory(event, &types.SessionHistory{Mode: historyMode})
	application.Publish(event)
}
func emitSessionEnded(application *apppkg.App, sessionID, agentName, reason string) {
	application.Publish(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}
