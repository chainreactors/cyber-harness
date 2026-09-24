package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/cli/configuration"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	flags "github.com/jessevdk/go-flags"
)

type options struct {
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
	Debug       bool   `long:"debug" description:"Enable debug logging"`
	Verbose     []bool `short:"v" long:"verbose" description:"Increase output detail"`
	Quiet       bool   `short:"q" long:"quiet" description:"Only show the final result"`
	NoColor     bool   `long:"no-color" description:"Disable ANSI colors"`
	Version     bool   `long:"version" description:"Print version and exit"`
}

func main() {
	if code, handled := terminaltool.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (resultErr error) {
	if handled, err := configuration.Run(ctx, args, configuration.Host{Name: "agent", Out: stdout, Err: stderr}); handled {
		return err
	}
	parsed, option, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	if parsed.Version {
		fmt.Fprintf(stdout, "agent v%s\n", cfg.Version)
		return nil
	}
	option.Context = &cfg.Context{Directory: parsed.WorkDir}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
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
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Debug: option.Debug, Quiet: option.Quiet, Output: stderr, Color: !option.NoColor})
	profile, err := newAgentProfile(option, logger, workDir, parsed.BashTimeout)
	if err != nil {
		return err
	}
	if err := profile.Load(ctx); err != nil {
		return errors.Join(err, profile.Close(context.Background()))
	}
	defer func() { resultErr = errors.Join(resultErr, profile.Close(context.Background())) }()

	runCtx := ctx
	if option.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(option.Timeout)*time.Second)
		defer cancel()
	}
	if !cfg.HasAgentOneShotInput(&option) {
		if _, err := profile.runtime.Skills().ApplySelected("", option.Skills); err != nil {
			return err
		}
		return console.AttachLocalREPL(runCtx, profile.runtime, &option, profile.ConsoleBindings())
	}
	task, err := cfg.ResolveTask(&option)
	if err != nil {
		return err
	}
	task = skills.ExpandCommand(task, profile.runtime.Skills())
	task, err = profile.runtime.Skills().ApplySelected(task, option.Skills)
	if err != nil {
		return err
	}
	return console.RunTask(runCtx, profile.runtime, &option, "task", "task", task, agentsession.RunInput{
		Content: []*aop.Content{aop.Text(task)}, EvalCriteria: option.EvalCriteria, EvalRounds: option.EvalRounds,
	}, nil)
}

func parseOptions(args []string, stderr io.Writer) (options, cfg.Option, error) {
	var parsed options
	parser := flags.NewParser(&parsed, flags.Default&^flags.PrintErrors)
	configuration.RegisterHelp(parser)
	parser.SubcommandsOptional = true
	parser.Name = "agent"
	parser.Usage = "[OPTIONS]"
	rest, err := parser.ParseArgs(args)
	if err != nil {
		return parsed, cfg.Option{}, err
	}
	if len(rest) != 0 {
		return parsed, cfg.Option{}, fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}
	option := cfg.Option{
		LLMOptions: parsed.LLMOptions,
		AgentOptions: cfg.AgentOptions{
			Prompt: parsed.Prompt, Inputs: parsed.Inputs, Skills: parsed.Skills,
			TaskFile: parsed.TaskFile, Heartbeat: parsed.Heartbeat, Timeout: parsed.Timeout,
			EvalCriteria: parsed.EvalCriteria, EvalModel: parsed.EvalModel, EvalRounds: parsed.EvalRounds,
			Resume: parsed.Resume, CaptureProviderFrames: parsed.CaptureFrames, Transport: string(cfg.AgentTransportLocal),
		},
		NodeOptions: cfg.NodeOptions{NodeName: parsed.NodeName},
		MiscOptions: cfg.MiscOptions{
			ConfigFile: parsed.ConfigFile, DataDir: parsed.DataDir,
			OutputFormat: parsed.Format, JSON: parsed.JSON, Debug: parsed.Debug,
			Verbose: parsed.Verbose, Quiet: parsed.Quiet, NoColor: parsed.NoColor, Version: parsed.Version,
		},
	}
	cfg.CaptureExplicitFlags(&option, parser)
	option.MarkExplicit("transport")
	return parsed, option, nil
}
