package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	provider "github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	profile "github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/tools/scan"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/cyber/tools/toolargs"
	"github.com/chainreactors/utils/proc"
)

// ---------------------------------------------------------------------------
// Mode dispatch
// ---------------------------------------------------------------------------

func runAgentMode(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, setInterrupt func(func() bool), outputTask *taskcli.Output) error {
	if !cfg.HasAgentOneShotInput(option) {
		if option.OutputFile != "" && option.OutputFormat != "jsonl" {
			return fmt.Errorf("interactive output stays in the terminal; use -o jsonl -f <file> to record events")
		}
		if option != nil && option.OutputFormat != "" && option.OutputFormat != "text" && !(option.OutputFormat == "jsonl" && option.OutputFile != "") {
			return fmt.Errorf("--output-format=%s is only available for one-shot agent runs", option.OutputFormat)
		}
		return runInteractiveMode(ctx, newProfile, option, logger, setInterrupt, outputTask)
	}
	return runOneShotMode(ctx, newProfile, option, logger, outputTask)
}

// ---------------------------------------------------------------------------
// Agent one-shot
// ---------------------------------------------------------------------------

func runOneShotMode(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, outputTask *taskcli.Output) (runErr error) {
	task, err := cfg.ResolveTask(option)
	if err != nil {
		return err
	}

	p, rt, err := loadAgentProfile(ctx, newProfile, option, logger, &agentsession.Config{Loop: agent.StandardLoop{}})
	if err != nil {
		return err
	}
	finish := profile.CloseOnce(ctx, p.Close)
	defer func() { runErr = finish(runErr) }()

	if option.OutputFormat == "jsonl" {
		outputTask.Observe(rt)
	}
	task = skills.ExpandCommand(task, rt.Skills())
	task, err = rt.Skills().ApplySelected(task, option.Skills)
	if err != nil {
		return err
	}

	return outputTask.Run(ctx, rt, "task", "task", task, agentsession.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalRounds: option.EvalRounds,
	}, finish)
}

// ---------------------------------------------------------------------------
// Agent interactive (REPL)
// ---------------------------------------------------------------------------

func runInteractiveMode(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, setInterrupt func(func() bool), outputTask *taskcli.Output) (runErr error) {
	p, rt, err := loadAgentProfile(ctx, newProfile, option, logger, &agentsession.Config{
		PrimarySessionID: console.MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		return err
	}
	finish := profile.CloseOnce(ctx, p.Close)
	defer func() { runErr = finish(runErr) }()
	if option.OutputFormat == "jsonl" {
		outputTask.Observe(rt)
	}

	if _, err := rt.Skills().ApplySelected("", option.Skills); err != nil {
		return err
	}

	if setInterrupt != nil {
		setInterrupt(func() bool { return false })
	}
	return console.AttachLocalREPL(ctx, rt, option, p.ConsoleBindings())
}

// ---------------------------------------------------------------------------
// Scanner direct execution
// ---------------------------------------------------------------------------

// shellHost is a profile that publishes a command surface. It is declared here
// because this is the only caller that needs one; a host that cannot run
// commands simply does not implement it.
type shellHost interface {
	Shell() (coretool.CommandExecutor, *terminaltool.BashTool)
}

func runDirectScannerMode(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, rest []string, logger telemetry.Logger, outputTask *taskcli.Output) (runErr error) {
	if cfg.IsScannerHelpRequest(rest) {
		if usage, ok := productScannerUsage(rest[0]); ok {
			_, err := fmt.Fprint(outputTask.Stdout(), usage)
			if err != nil {
				return err
			}
			if !strings.HasSuffix(usage, "\n") {
				_, err = fmt.Fprintln(outputTask.Stdout())
			}
			return err
		}
	}
	mode, scannerArgs, err := configuredScannerMode(option, rest)
	if err != nil {
		return err
	}
	if option.AI || mode.Agent {
		mode.Provider = provider.StartupRequired
	}
	scannerLogger := logger
	if !directScannerDebugEnabled(option, scannerArgs) {
		scannerLogger = telemetry.ErrorOnlyLogger(logger)
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
	}

	var sessionConfig *agentsession.Config
	if option.AI && scannerArgs[0] != "scan" {
		sessionConfig = &agentsession.Config{
			Loop: agent.StandardLoop{}, PromptTarget: scan.ScannerSystemTarget,
			CommandName: scannerArgs[0], SkipBaseSkills: true,
		}
	}
	if newProfile == nil {
		return fmt.Errorf("profile constructor is required")
	}
	p, err := newProfile(profile.Request{
		Option: option, ProviderMode: mode.Provider, Session: sessionConfig, Logger: scannerLogger,
	})
	if err != nil {
		return fmt.Errorf("construct scanner profile: %w", err)
	}
	if p == nil {
		return fmt.Errorf("profile constructor returned nil")
	}
	finish := profile.CloseOnce(ctx, p.Close)
	defer func() { runErr = finish(runErr) }()
	if err := p.Load(ctx); err != nil {
		return fmt.Errorf("load scanner profile: %w", err)
	}
	providers, err := p.Providers()
	if err != nil {
		return err
	}
	_, providerConfig := providers.Current()
	cfg.ApplyResolvedProviderOptions(option, providerConfig)
	application, err := p.Events()
	if err != nil {
		return err
	}

	outputTask.Observe(application)

	// A host without a session runtime still runs commands: the registry and
	// the tool are capabilities its graph publishes either way.
	host, ok := p.(shellHost)
	if !ok {
		return fmt.Errorf("profile does not expose a command surface")
	}
	registry, bash := host.Shell()
	if registry == nil || bash == nil {
		return fmt.Errorf("bash tool is not registered")
	}
	if !registry.Has(scannerArgs[0]) {
		return fmt.Errorf("unknown subcommand: %s", scannerArgs[0])
	}
	if option.Debug && scannerCommandSupportsDebug(scannerArgs[0]) && !toolargs.BoolFlagEnabled(scannerArgs[1:], "--debug") {
		scannerArgs = append(scannerArgs, "--debug")
	}

	if option.AI && scannerArgs[0] != "scan" {
		runtime, runtimeErr := p.Runtime()
		if runtimeErr != nil {
			return runtimeErr
		}
		return runScannerWithAgent(ctx, option, runtime, scannerArgs, logger, outputTask, finish)
	}

	if option.NoColor && scannerArgs[0] == "scan" && !toolargs.HasFlag(scannerArgs[1:], "--no-color") {
		scannerArgs = append(scannerArgs, "--no-color")
	}
	sessionID := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	turnID := sessionID + "-run"
	emitter := scannerArgs[0]
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
		stopReason := string(agenthooks.StopReasonCompleted)
		closeReason := agentsession.SessionCloseCompleted
		if runErr != nil {
			result.Output = []*aop.Content{aop.Text(runErr.Error())}
			stopReason = string(agenthooks.StopReasonError)
			closeReason = agentsession.SessionCloseError
		}
		if isCanceled {
			stopReason = string(agenthooks.StopReasonCanceled)
			closeReason = agentsession.SessionCloseCanceled
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
	scannerOutput := outputTask.Stdout()
	if option.OutputFormat != "" && option.OutputFormat != "text" {
		scannerOutput = io.Discard
	}
	streaming := shouldStreamScannerOutput(scannerArgs)
	var outputErr error
	var captured strings.Builder
	execution, err := bash.RunForeground(ctx, coretool.JoinCommandLine(scannerArgs[0], scannerArgs[1:]), terminaltool.BashExecOptions{
		OnOutput: func(data []byte) {
			if streaming {
				if outputErr == nil {
					var n int
					n, outputErr = scannerOutput.Write(data)
					if outputErr == nil && n != len(data) {
						outputErr = io.ErrShortWrite
					}
				}
			} else {
				_, _ = captured.Write(data)
			}
		},
	})
	if err != nil {
		return errors.Join(err, outputErr)
	}
	if outputErr != nil {
		return outputErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !streaming {
		output := captured.String()
		if isDirectScannerJSONOutput(scannerArgs) {
			output = filterScannerJSONLines(output)
		}
		if _, err := io.WriteString(scannerOutput, output); err != nil {
			return err
		}
	}
	info, retained := execution.Session()
	if !retained && execution.ID != "" {
		return fmt.Errorf("command session %s is no longer available", execution.ID)
	}
	// A built-in command runs in-process, so it has no exit code and the exit
	// status reads zero however the command ended. The unit's terminal state is
	// the failure, and Reason carries the tool's own error text.
	if retained && info.State != proc.StateCompleted {
		if info.Reason != "" {
			return errors.New(info.Reason)
		}
		return fmt.Errorf("%s %s (exit code %d)", scannerArgs[0], info.State, info.ExitStatus())
	}
	return nil
}

// filterScannerJSONLines keeps the public --json contract stable when a
// scanner engine writes progress text through the PTY alongside its records.
// The scan command emits one compact JSON value per line; progress banners,
// tables, and summaries are deliberately excluded from stdout in this mode.
func filterScannerJSONLines(raw string) string {
	var out strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || (line[0] != '{' && line[0] != '[') || !json.Valid([]byte(line)) {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
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

func emitSessionStarted(application aop.EventPublisher, sessionID, agentName string, started *aop.SessionStarted, historyMode types.SessionHistory_Mode) {
	event := &aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionStarted{SessionStarted: started}}
	_ = types.SetSessionHistory(event, &types.SessionHistory{Mode: historyMode})
	application.Publish(event)
}
func emitSessionEnded(application aop.EventPublisher, sessionID, agentName, reason string) {
	application.Publish(&aop.Event{SessionId: sessionID, Emitter: agentName, Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: reason}}})
}
