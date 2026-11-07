package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/cmd/audit/internal/toolchain"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/cli/configuration"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	"github.com/chainreactors/cyber/pkg/node"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	flags "github.com/jessevdk/go-flags"
)

type options struct {
	ReportDir      string `long:"report-dir" description:"New report directory (default: <workdir>/.cyber/audit/<run-id>)"`
	cfg.LLMOptions `group:"LLM Options"`
	Prompt         string   `short:"p" long:"prompt" description:"Natural language task or existing file path"`
	Inputs         []string `short:"i" long:"input" description:"Task input. Can be specified multiple times"`
	Skills         []string `short:"s" long:"skill" description:"Skill name or file path. Can be specified multiple times"`
	TaskFile       string   `long:"task-file" description:"File containing the task description"`
	Heartbeat      int      `long:"heartbeat" description:"Heartbeat interval in minutes (0 disables)" default:"0"`
	Timeout        int      `long:"timeout" description:"Overall timeout in seconds (0 disables)" default:"3600"`
	EvalCriteria   string   `short:"e" long:"eval" description:"Goal evaluation criteria"`
	EvalModel      string   `long:"eval-model" description:"Goal evaluation model"`
	EvalRounds     string   `long:"eval-rounds" description:"How long goal evaluation may keep going: a number (hard ceiling) or plain language the evaluator follows"`
	Resume         string   `short:"r" long:"resume" description:"Resume from an AOP JSONL session file"`
	CaptureFrames  bool     `long:"capture-provider-frames" description:"Emit exact provider frames as sensitive events"`

	ConfigFile  string `short:"c" long:"config" description:"Path to cyber.yaml"`
	DataDir     string `long:"data-dir" description:"Data directory"`
	WorkDir     string `long:"workdir" description:"Workspace exposed to file and bash tools"`
	BashTimeout int    `long:"bash-timeout" description:"Default bash timeout in seconds" default:"600"`
	Format      string `short:"o" long:"output-format" description:"Output format: text, json, jsonl, markdown, csv" default:"text"`
	File        string `short:"f" long:"file" description:"Output destination (default: stdout)"`
	JSON        bool   `long:"json" description:"Alias for --output-format=json"`
	NodeName    string `long:"node-name" description:"Agent node name"`
	NodeID      string `long:"node-id" description:"Existing node ID"`
	ServerURL   string `long:"server-url" description:"Cyber Web server URL for node enrollment"`
	Debug       bool   `long:"debug" description:"Enable debug logging"`
	Verbose     []bool `short:"v" long:"verbose" description:"Increase output detail"`
	Quiet       bool   `short:"q" long:"quiet" description:"Only show the final result"`
	NoColor     bool   `long:"no-color" description:"Disable ANSI colors"`
	Version     bool   `long:"version" description:"Print version and exit"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return run(ctx, args, stdout, stderr, (*toolchain.Manager).Ensure)
}
func run(ctx context.Context, args []string, stdout, stderr io.Writer, ensure func(*toolchain.Manager, context.Context, io.Writer) ([]toolchain.Status, error)) (err error) {
	args = toolCommandFirst(args)
	if len(args) > 0 && args[0] == "validate" {
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			_, err := fmt.Fprintln(stdout, "Usage: cyber-audit validate <report-directory>\nRead-only check of coverage, findings, evidence paths and OKF; no model credentials or tool installation required.")
			return err
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: cyber-audit validate <report-directory>")
		}
		return validateReport(ctx, args[1], stdout)
	}
	if handled, err := runToolCommand(ctx, args, stdout, stderr); handled {
		return err
	}
	if handled, err := configuration.Run(ctx, args, configuration.Host{Name: "cyber-audit", Context: &cfg.Context{UserLLMOnly: true}, Out: stdout, Err: stderr}); handled {
		return err
	}
	parsed, option, err := parseOptions(args, stdout)
	output := taskcli.NewOutput(&option, stdout, stderr)
	defer func() { err = output.Finish(err) }()
	if err != nil {
		return err
	}
	if parsed.Version {
		fmt.Fprintf(stdout, "cyber-audit v%s\n", cfg.Version)
		return nil
	}
	if err := output.Open(); err != nil {
		return err
	}
	if _, err := cfg.ResolveAgentRuntimeConfig(&option); err != nil {
		return err
	}
	transport, err := cfg.ResolveAgentTransport(&option)
	if err != nil {
		return err
	}
	if option.Snapshot != nil {
		for _, message := range option.Snapshot.Diagnostics {
			fmt.Fprintln(stderr, message)
		}
	}
	workDir := strings.TrimSpace(parsed.WorkDir)
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return err
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workdir must be a directory")
	}
	task, oneShot, err := resolveAuditTask(&option)
	if err != nil {
		return err
	}
	if transport == cfg.AgentTransportWeb && (option.OutputFormat != "text" || option.OutputFile != "") {
		return fmt.Errorf("-o/-f select task output; the server exposes events through its transport")
	}
	if !oneShot && transport != cfg.AgentTransportWeb {
		if option.OutputFile != "" && option.OutputFormat != "jsonl" {
			return fmt.Errorf("interactive output stays in the terminal; use -o jsonl -f <file> to record events")
		}
		if option.OutputFormat != "text" && !(option.OutputFormat == "jsonl" && option.OutputFile != "") {
			return fmt.Errorf("--output-format=%s is only available for one-shot agent runs", option.OutputFormat)
		}
	}
	if transport != cfg.AgentTransportWeb && option.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(option.Timeout)*time.Second)
		defer cancel()
	}
	if parsed.BashTimeout <= 0 {
		return fmt.Errorf("--bash-timeout must be positive")
	}
	manager, err := toolchain.New(option.DataDir)
	if err != nil {
		return err
	}
	statuses, err := ensure(manager, ctx, stderr)
	if err != nil {
		return err
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Debug: option.Debug, Quiet: option.Quiet, Output: stderr, Color: !option.NoColor})
	if transport == cfg.AgentTransportWeb {
		build := func(request profilepkg.Request) (profilepkg.Profile, error) {
			return newAuditProfile(request, workDir, parsed.BashTimeout, nil, manager.Manager, statuses)
		}
		return node.RunWebSocketWithCapabilities(ctx, build, &option, logger, "audit")
	}
	report, err := newReport(ctx, workDir, parsed.ReportDir, task, option.Resume, statuses)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Audit report: %s\n", report.Directory)
	profile, err := newAuditProfile(profilepkg.Request{Option: &option, ProviderMode: provider.StartupRequired, Logger: logger, Session: &agentsession.Config{PrimarySessionID: "main", Loop: agent.StandardLoop{}}}, workDir, parsed.BashTimeout, report, manager.Manager, report.Tools)
	if err != nil {
		return report.finish(ctx, err)
	}
	closeProfile := profilepkg.CloseOnce(ctx, profile.Close)
	finish := func(runErr error) error { return report.finish(ctx, closeProfile(runErr)) }
	if err := profile.Load(ctx); err != nil {
		return finish(err)
	}
	if option.OutputFormat == "jsonl" {
		output.Observe(profile.runtime)
	}
	if !oneShot {
		if _, err := profile.runtime.Skills().ApplySelected("", option.Skills); err != nil {
			return finish(err)
		}
		return finish(console.AttachLocalREPL(ctx, profile.runtime, &option, profile.ConsoleBindings()))
	}
	task = skills.ExpandCommand(task, profile.runtime.Skills())
	task, err = profile.runtime.Skills().ApplySelected(task, option.Skills)
	if err != nil {
		return finish(err)
	}
	output.Validation = console.TaskValidation{Check: report.validate, MaxRepairRounds: 2,
		RepairInstruction: "Repair only the assigned audit report and its evidence. Read cyber://skills/audit/report.md and run `audit validate` for the complete contract (including JSON, evidence paths and OKF). Preserve source code and existing findings; do not restart investigation or repeat successful scans. Explain reused evidence in notes; check status remains completed, incomplete or not_applicable."}
	return output.Run(ctx, profile.runtime, "task", "audit", task, agentsession.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalRounds: option.EvalRounds,
	}, finish)
}

func parseOptions(args []string, helpOutput io.Writer) (options, cfg.Option, error) {
	var parsed options
	parser := flags.NewParser(&parsed, flags.Default&^flags.PrintErrors)
	configuration.RegisterHelp(parser)
	parser.SubcommandsOptional = true
	parser.Name = "cyber-audit"
	parser.Usage = "[OPTIONS]"
	parser.LongDescription = "Model-led code audit. Shared model settings: CYBER_API_KEY, CYBER_BASE_URL, CYBER_MODEL, CYBER_PROVIDER.\n\nCommands:\n  validate <report-directory>  Check a complete report without starting a model\n  doctor                      Check required tools\n  tools install               Prepare required tools\n  config                      Inspect or configure shared Cyber settings"
	rest, err := parser.ParseArgs(args)
	if err != nil {
		var flagErr *flags.Error
		if errors.As(err, &flagErr) && flagErr.Type == flags.ErrHelp && helpOutput != nil {
			parser.WriteHelp(helpOutput)
		}
		return parsed, cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: parsed.Format, OutputFile: parsed.File, JSON: parsed.JSON}}, err
	}
	if len(rest) != 0 {
		return parsed, cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: parsed.Format, OutputFile: parsed.File, JSON: parsed.JSON}}, fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}
	option := cfg.Option{
		Context:    &cfg.Context{Directory: parsed.WorkDir, UserLLMOnly: true},
		LLMOptions: parsed.LLMOptions,
		AgentOptions: cfg.AgentOptions{
			Prompt: parsed.Prompt, Inputs: parsed.Inputs, Skills: parsed.Skills,
			TaskFile: parsed.TaskFile, Heartbeat: parsed.Heartbeat, Timeout: parsed.Timeout,
			EvalCriteria: parsed.EvalCriteria, EvalModel: parsed.EvalModel, EvalRounds: parsed.EvalRounds,
			Resume: parsed.Resume, CaptureProviderFrames: parsed.CaptureFrames, Transport: string(cfg.AgentTransportAuto), ServerURL: parsed.ServerURL,
		},
		NodeOptions: cfg.NodeOptions{NodeID: parsed.NodeID, NodeName: parsed.NodeName},
		MiscOptions: cfg.MiscOptions{
			ConfigFile: parsed.ConfigFile, DataDir: parsed.DataDir,
			OutputFormat: parsed.Format, OutputFile: parsed.File, JSON: parsed.JSON, Debug: parsed.Debug,
			Verbose: parsed.Verbose, Quiet: parsed.Quiet, NoColor: parsed.NoColor, Version: parsed.Version,
		},
	}
	cfg.CaptureExplicitFlags(&option, parser)
	return parsed, option, nil
}

// No scanner fallback: explicit task -> one-shot; otherwise use the local REPL.
func resolveAuditTask(option *cfg.Option) (string, bool, error) {
	task, err := cfg.ResolveTaskPrompt(option)
	if err != nil {
		return "", false, err
	}
	explicit := cfg.HasAgentTaskInput(option)
	if task == "" && len(option.Inputs) > 0 {
		task = "Audit the supplied source code or binaries for vulnerabilities and record evidence and coverage."
	}
	if len(option.Inputs) > 0 {
		task += "\n\nAudit inputs:\n" + strings.Join(option.Inputs, "\n")
	}
	return task, explicit, nil
}

func runToolCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (handled bool, resultErr error) {
	if len(args) == 0 || args[0] != "doctor" && args[0] != "tools" {
		return false, nil
	}
	install := args[0] == "tools"
	rest := args[1:]
	if install {
		if len(rest) == 0 || rest[0] != "install" {
			return true, fmt.Errorf("usage: cyber-audit tools install [--data-dir DIR]")
		}
		rest = rest[1:]
	}
	_, option, err := parseOptions(rest, stdout)
	if err != nil {
		return true, err
	}
	if _, err := cfg.ResolveToolRuntimeConfig(&option); err != nil {
		return true, err
	}
	format := option.OutputFormat
	if format != "text" && format != "json" {
		return true, fmt.Errorf("tool commands support -o text or -o json")
	}
	output := taskcli.NewOutput(&option, stdout, stderr)
	if err := output.Open(); err != nil {
		return true, err
	}
	defer func() { resultErr = output.Finish(resultErr) }()
	stdout = output.Stdout()
	if option.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(option.Timeout)*time.Second)
		defer cancel()
	}
	manager, err := toolchain.New(option.DataDir)
	if err != nil {
		return true, err
	}
	var statuses []toolchain.Status
	if install {
		statuses, err = manager.Ensure(ctx, stderr)
	} else {
		statuses = manager.Check(ctx)
	}
	failed := false
	for _, status := range statuses {
		failed = failed || status.Error != ""
	}
	if failed && err == nil {
		err = fmt.Errorf("required tools unavailable; run cyber-audit tools install")
	}
	err = errors.Join(err, ctx.Err())
	if format == "json" {
		result := struct {
			Tools   []toolchain.Status `json:"tools"`
			IsError bool               `json:"is_error"`
			Error   string             `json:"error,omitempty"`
		}{Tools: statuses, IsError: err != nil}
		if err != nil {
			result.Error = err.Error()
		}
		return true, errors.Join(err, json.NewEncoder(stdout).Encode(result))
	}
	for _, status := range statuses {
		var writeErr error
		if status.Error != "" {
			_, writeErr = fmt.Fprintf(stdout, "%s: %s\n", status.Name, status.Error)
		} else {
			_, writeErr = fmt.Fprintf(stdout, "%s %s: %s\n", status.Name, status.Version, status.Path)
		}
		if writeErr != nil {
			return true, errors.Join(err, writeErr)
		}
	}
	return true, err
}

// Otherwise --data-dir X doctor falls into the shared config doctor's route,
// while doctor --data-dir X checks audit's installed tools.
func toolCommandFirst(args []string) []string {
	var parsed options
	parser := flags.NewParser(&parsed, flags.PassAfterNonOption)
	rest, err := parser.ParseArgs(args)
	if err != nil || len(rest) == 0 {
		return args
	}
	index := len(args) - len(rest)
	if index == 0 {
		return args
	}
	switch rest[0] {
	case "doctor":
		return append(append([]string{"doctor"}, args[:index]...), rest[1:]...)
	case "tools":
		if len(rest) > 1 && rest[1] == "install" {
			return append(append([]string{"tools", "install"}, args[:index]...), rest[2:]...)
		}
	}
	return args
}
