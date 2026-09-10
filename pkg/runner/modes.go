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
	"github.com/chainreactors/aiscan/core/telemetry"
	coretool "github.com/chainreactors/aiscan/core/tool"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	cmdpkg "github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/console"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	"github.com/chainreactors/aiscan/tools/toolargs"
)

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

	rt, err := runtimepkg.New(ctx, option, logger, &runtimepkg.RuntimeConfig{})
	if err != nil {
		return err
	}
	defer rt.Close()

	task = skills.ExpandCommand(task, rt.App().Skills)
	task, err = cfg.ApplySelectedSkills(task, option.Skills, rt.App().Skills)
	if err != nil {
		return err
	}

	return console.RunTask(ctx, rt, option, "task", "task", task, runtimepkg.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalMaxRounds: option.EvalMaxRetries,
	})
}

// ---------------------------------------------------------------------------
// Agent interactive (REPL)
// ---------------------------------------------------------------------------

func runInteractiveMode(ctx context.Context, option *cfg.Option, logger telemetry.Logger, setInterrupt func(func() bool)) error {
	option.SaveSession = true
	rt, err := runtimepkg.New(ctx, option, logger, &runtimepkg.RuntimeConfig{
		PrimarySessionID: console.MainREPLName,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	if _, err := cfg.ApplySelectedSkills("", option.Skills, rt.App().Skills); err != nil {
		return err
	}

	if setInterrupt != nil {
		setInterrupt(func() bool { return false })
	}
	return console.AttachLocalREPL(ctx, rt, option)
}

// ---------------------------------------------------------------------------
// Scanner direct execution
// ---------------------------------------------------------------------------

func RunDirectScannerMode(ctx context.Context, option *cfg.Option, rest []string, logger telemetry.Logger) (runErr error) {
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
		if usage, ok := cfg.StaticScannerUsage(scannerArgs[0]); ok {
			fmt.Print(usage)
			if !strings.HasSuffix(usage, "\n") {
				fmt.Println()
			}
			return nil
		}
	}
	if recordPath := runtimepkg.ResolveJSONLRecordPath(option); recordPath != "" {
		option.OutputFile = recordPath
	}

	scannerLogger := logger
	if !directScannerDebugEnabled(option, scannerArgs) {
		scannerLogger = telemetry.ErrorOnlyLogger(logger)
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
	}

	application, err := apppkg.New(ctx, apppkg.AppConfig(option, features, scannerLogger))
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer application.Close()
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
	tool, ok := application.Commands.GetTool("bash")
	if !ok {
		return fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*cmdpkg.BashTool)
	if !ok {
		return fmt.Errorf("registered bash tool has unexpected type")
	}
	callID := turnID + "-call"
	ctx = coretool.ContextWithInvocation(ctx, coretool.Invocation{
		CallID: callID, SessionID: sessionID, TurnID: turnID, Emitter: emitter,
	})
	arguments, err := aop.JSONValue(map[string]any{"args": scannerArgs[1:]})
	if err != nil {
		return fmt.Errorf("encode scanner arguments: %w", err)
	}
	startedAt := time.Now()
	if application.EventBus != nil {
		emitSessionStarted(application, sessionID, emitter, &aop.SessionStarted{}, types.SessionHistory_MODE_INHERIT)
		application.Emit(&aop.Event{
			SessionId: sessionID, TurnId: turnID, Emitter: emitter,
			Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}},
		})
		application.Emit(&aop.Event{
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
			closeReason := runtimepkg.SessionCloseCompleted
			if runErr != nil {
				result.Output = []*aop.Content{aop.Text(runErr.Error())}
				stopReason = string(agent.StopReasonError)
				closeReason = runtimepkg.SessionCloseError
			}
			if isCanceled {
				stopReason = string(agent.StopReasonCanceled)
				closeReason = runtimepkg.SessionCloseCanceled
			}
			application.Emit(&aop.Event{
				SessionId: sessionID, TurnId: turnID, Emitter: emitter,
				Payload: &aop.Event_ToolResult{ToolResult: result},
			})
			application.Emit(&aop.Event{
				SessionId: sessionID, TurnId: turnID, Emitter: emitter,
				Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: stopReason}},
			})
			emitSessionEnded(application, sessionID, emitter, string(closeReason))
		}()
	}
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
	if execution.ExitCode != 0 {
		return fmt.Errorf("%s exited with code %d", scannerArgs[0], execution.ExitCode)
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
	application.Emit(event)
}
func emitSessionEnded(application *apppkg.App, sessionID, agentName, reason string) {
	application.Emit(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}
