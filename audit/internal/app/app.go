package app

import (
	"context"
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
	"github.com/chainreactors/cyber/audit/internal/toolchain"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/cli/configuration"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	"github.com/chainreactors/cyber/pkg/node"
	"github.com/chainreactors/cyber/pkg/profile"
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
	Format      string `long:"output-format" description:"One-shot output: text, json, or stream-json" default:"text"`
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
func run(ctx context.Context, args []string, stdout, stderr io.Writer, ensure func(*toolchain.Manager, context.Context, io.Writer) ([]toolchain.Status, error)) error {
	if handled, err := runToolCommand(ctx, args, stdout, stderr); handled {
		return err
	}
	if handled, err := configuration.Run(ctx, args, configuration.Host{Name: "cyber-audit", Context: &cfg.Context{UserLLMOnly: true}, Out: stdout, Err: stderr}); handled {
		return err
	}
	parsed, option, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	if parsed.Version {
		fmt.Fprintf(stdout, "cyber-audit v%s\n", cfg.Version)
		return nil
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
	if parsed.JSON {
		option.OutputFormat = "json"
	}
	switch option.OutputFormat {
	case "text", "json", "stream-json":
	default:
		return fmt.Errorf("unsupported --output-format %q", option.OutputFormat)
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
	if transport != cfg.AgentTransportWeb && option.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(option.Timeout)*time.Second)
		defer cancel()
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
		if oneShot || option.Resume != "" {
			return fmt.Errorf("node mode receives audit tasks from the server; omit --prompt, --input, --task-file and --resume")
		}
		build := func(request profile.Request) (profile.Profile, error) {
			return newAuditProfile(request, workDir, parsed.BashTimeout, nil, manager.Manager, statuses)
		}
		return node.RunWebSocket(ctx, build, &option, logger)
	}
	report, err := newReport(ctx, workDir, parsed.ReportDir, task, option.Resume, statuses)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Audit report: %s\n", report.Directory)
	profile, err := newAuditProfile(profile.Request{Option: &option, ProviderMode: provider.StartupRequired, Logger: logger, Session: &agentsession.Config{PrimarySessionID: "main", Loop: agent.StandardLoop{}}}, workDir, parsed.BashTimeout, report, manager.Manager, report.Tools)
	if err != nil {
		return report.finish(ctx, err)
	}
	finish := func(runErr error) error {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return report.finish(ctx, errors.Join(runErr, profile.Close(closeCtx)))
	}
	if err := profile.Load(ctx); err != nil {
		return finish(err)
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
	return console.RunTask(ctx, profile.runtime, &option, "task", "audit", task, agentsession.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalRounds: option.EvalRounds,
	}, finish)
}

func parseOptions(args []string, stderr io.Writer) (options, cfg.Option, error) {
	var parsed options
	parser := flags.NewParser(&parsed, flags.Default&^flags.PrintErrors)
	configuration.RegisterHelp(parser)
	parser.SubcommandsOptional = true
	parser.Name = "cyber-audit"
	parser.Usage = "[OPTIONS]"
	rest, err := parser.ParseArgs(args)
	if err != nil {
		return parsed, cfg.Option{}, err
	}
	if len(rest) != 0 {
		return parsed, cfg.Option{}, fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
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
			OutputFormat: parsed.Format, JSON: parsed.JSON, Debug: parsed.Debug,
			Verbose: parsed.Verbose, Quiet: parsed.Quiet, NoColor: parsed.NoColor, Version: parsed.Version,
		},
	}
	cfg.CaptureExplicitFlags(&option, parser)
	return parsed, option, nil
}

// No scanner fallback: explicit task -> one-shot; otherwise use the local REPL.
func resolveAuditTask(option *cfg.Option) (string, bool, error) {
	if option.Prompt != "" && option.TaskFile != "" {
		return "", false, fmt.Errorf("use either --prompt or --task-file")
	}
	task, err := cfg.ResolvePrompt(option.Prompt)
	if err != nil {
		return "", false, err
	}
	if option.TaskFile != "" {
		body, err := os.ReadFile(option.TaskFile)
		if err != nil {
			return "", false, err
		}
		task = strings.TrimSpace(string(body))
	}
	explicit := option.Prompt != "" || option.TaskFile != "" || len(option.Inputs) > 0
	if task == "" && len(option.Inputs) > 0 {
		task = "Audit the supplied source code or binaries for vulnerabilities and record evidence and coverage."
	}
	if len(option.Inputs) > 0 {
		task += "\n\nAudit inputs:\n" + strings.Join(option.Inputs, "\n")
	}
	if explicit && strings.TrimSpace(task) == "" {
		return "", false, fmt.Errorf("audit task is empty")
	}
	return task, explicit, nil
}

func runToolCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (bool, error) {
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
	_, option, err := parseOptions(rest, stderr)
	if err != nil {
		return true, err
	}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		return true, err
	}
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
		if status.Error != "" {
			fmt.Fprintf(stdout, "%s: %s\n", status.Name, status.Error)
			failed = true
		} else {
			fmt.Fprintf(stdout, "%s %s: %s\n", status.Name, status.Version, status.Path)
		}
	}
	if err != nil {
		return true, err
	}
	if failed {
		return true, fmt.Errorf("required tools unavailable; run cyber-audit tools install")
	}
	return true, nil
}
