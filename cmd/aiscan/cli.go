package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	"github.com/chainreactors/cyber/core/telemetry"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	sessionext "github.com/chainreactors/cyber/exts/session"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	"github.com/chainreactors/cyber/pkg/cli/configuration"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/output"

	goflags "github.com/jessevdk/go-flags"
)

const runModeWeb cfg.RunMode = "web"

func cliCommandSummary() string {
	base := "agent, inbox, web, serve"
	summaries := productScannerNames()
	if len(summaries) == 0 {
		return base
	}
	return base + ", " + strings.Join(summaries, ", ")
}

type webCommand struct {
	Addr            string `long:"addr" default:"127.0.0.1:8080" description:"HTTP listen address"`
	DB              string `long:"db" default:"cyber-web.db" description:"SQLite database path"`
	MaxScans        int    `long:"max-scans" default:"3" description:"Maximum concurrent scans"`
	ScanTimeout     int    `long:"scan-timeout" default:"600" description:"Maximum scan runtime in seconds"`
	Token           string `long:"token" description:"Access key for the server (auto-generated if empty)"`
	NoAgent         bool   `long:"no-agent" description:"Start the web console only, without the embedded agent node"`
	cfg.LLMOptions  `group:"LLM Options"`
	cfg.NodeOptions `group:"Server Options"`
}

type cliOptions struct {
	registry        *hostcli.Registry `no-flag:"true"`
	cfg.MiscOptions `group:"Miscellaneous Options"`
	Timeout         int              `long:"timeout" description:"Overall timeout in seconds"`
	Agent           agentCommand     `command:"agent" description:"Run the natural-language agent"`
	Inbox           agentCommand     `command:"inbox" description:"Run the persistent message router and subagent host"`
	InboxOptions    cfg.InboxOptions `no-flag:"true"`
	Web             webCommand       `command:"web" description:"Start the web UI server (includes embedded agent server)"`
}

type agentCommand struct {
	cfg.LLMOptions   `group:"LLM Options"`
	cfg.AgentOptions `no-flag:"true"`
	cfg.NodeOptions  `group:"Server Options"`
}

func (agentCommand) Usage() string { return "[OPTIONS]" }

type parsedCLI struct {
	Option      cfg.Option
	Mode        cfg.RunMode
	ScannerArgs []string
	Action      *hostcli.Action
	WebOpts     webCommand
	Help        bool
}

func cyber() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := setupSignalHandler(cancel, telemetry.NopLogger())
	if err := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, signals.SetStopFunc); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

// runCLI returns only after resources have closed. Process exit belongs to main.
func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, setInterrupt func(func() bool)) (resultErr error) {
	if handled, err := configuration.Run(ctx, args, configuration.Host{Name: "aiscan", Sections: defaultSections(), Checks: configChecks, Out: stdout, Err: stderr}); handled {
		return err
	}
	if len(args) == 0 {
		var cli cliOptions
		writeHelp(newCLIParser(&cli, goflags.Default&^goflags.PrintErrors), stdout)
		return nil
	}
	parsed, err := parseCLI(args, stdout)
	option := parsed.Option
	taskOutput := taskcli.NewOutput(&option, stdout, stderr)
	defer func() {
		resultErr = taskOutput.Finish(resultErr)
	}()
	if err != nil {
		return err
	}
	explicitOption := option
	if option.Version {
		_, err := fmt.Fprintf(stdout, "aiscan v%s\n", cfg.Version)
		return err
	}
	if option.ViewFile != "" {
		return output.RenderEventFile(option.ViewFile, option.OutputFormat, option.OutputFile, stdout)
	}
	if parsed.Help {
		return nil
	}
	if parsed.Mode == cfg.RunModeNoCommand && parsed.Action == nil {
		return fmt.Errorf("missing subcommand: use %s", cliCommandSummary())
	}
	if (parsed.Mode == runModeWeb || parsed.Action != nil && parsed.Action.Persistent) && (option.OutputFormat != "text" || option.OutputFile != "") {
		return fmt.Errorf("-o/-f select task or replay output; this server exposes events through its transport")
	}
	if err := taskOutput.Open(); err != nil {
		return err
	}
	resolveConfig := cfg.ResolveRuntimeConfig
	if parsed.Mode == cfg.RunModeAgent || parsed.Mode == cfg.RunModeInbox {
		resolveConfig = cfg.ResolveAgentRuntimeConfig
	}
	if parsed.Mode == cfg.RunModeScanner {
		resolveConfig = func(option *cfg.Option) (string, error) {
			return resolveScannerRuntimeConfig(option, parsed.ScannerArgs)
		}
	}
	cfgPath, err := resolveConfig(&option)
	if err != nil {
		return err
	}
	if err := applyIdentity(&option); err != nil {
		return err
	}
	if cfgPath != "" && option.Debug {
		fmt.Fprintf(stderr, "loaded config: %s\n", cfgPath)
	}
	if option.Snapshot != nil {
		for _, message := range option.Snapshot.Diagnostics {
			fmt.Fprintln(stderr, message)
		}
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Debug: option.Debug, Quiet: option.Quiet, Output: stderr, Color: !option.NoColor})
	if parsed.Mode != runModeWeb && !(parsed.Action != nil && parsed.Action.Persistent) && option.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(option.Timeout)*time.Second)
		defer cancel()
	}
	if parsed.Action != nil {
		return parsed.Action.Run(ctx, hostcli.Environment{Config: &option, Logger: logger, Out: taskOutput.Stdout(), Err: stderr})
	}
	switch parsed.Mode {
	case cfg.RunModeAgent:
		return runAgentTransport(ctx, newAIScanProfile, &option, logger, stdin, stdout, setInterrupt, taskOutput)
	case cfg.RunModeInbox:
		return runInbox(ctx, newAIScanProfile, &option, logger, stdin, taskOutput.Stdout(), taskOutput)
	case runModeWeb:
		return serveWeb(ctx, &option, &explicitOption, parsed.WebOpts, logger)
	case cfg.RunModeScanner:
		return runDirectScannerMode(ctx, newAIScanProfile, &option, parsed.ScannerArgs, logger, taskOutput)
	}
	return nil
}

func parseCLI(args []string, stdout io.Writer) (parsedCLI, error) {
	if scannerName, rootArgs, scannerRest, ok := splitScannerCommand(args); ok {
		return parseScannerCLI(scannerName, rootArgs, scannerRest, stdout)
	}

	var cli cliOptions
	parser := newCLIParser(&cli, parserOptionsForArgs(args))
	rest, err := parser.ParseArgs(args)
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			if scannerName := firstCommandName(args, rootFlagValueArity); productScannerAvailable(scannerName) {
				option := cfg.Option{MiscOptions: cli.MiscOptions}
				finalizeOptions(&option, nil)
				option.Timeout = 3600
				scannerArgs := append([]string{scannerName}, argsAfterCommand(args, scannerName)...)
				return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: scannerArgs}, nil
			}
			writeHelp(parser, stdout)
			return parsedCLI{Mode: cfg.RunModeNoCommand, Help: true}, nil
		}
		return parsedCLI{Option: buildOption(&cli, parser), Mode: selectedMode(parser)}, err
	}

	if cli.Version {
		return parsedCLI{Option: cfg.Option{MiscOptions: cli.MiscOptions}, Mode: cfg.RunModeNoCommand}, nil
	}

	mode := selectedMode(parser)
	option := buildOption(&cli, parser)
	cfg.CaptureExplicitFlags(&option, parser)
	action := cli.registry.Selected()
	option.Extensions = cli.registry.Values()
	finalizeOptions(&option, action)
	if flag := parser.Group.FindOptionByLongName("timeout"); flag != nil && flag.IsSet() {
		option.Timeout = cli.Timeout
	}
	if option.Timeout == 0 && !option.Explicit["timeout"] && mode != cfg.RunModeInbox {
		// Commands that own their options through the extension registry (the IOA
		// queries, for one) never receive AgentOptions, so nothing else supplies
		// this default. A zero deadline would cancel the context before the
		// command runs.
		option.Timeout = 3600
	}
	if err := cfg.ResolveOutputFormat(&option); err != nil {
		return parsedCLI{Option: option, Mode: mode}, err
	}

	if mode == cfg.RunModeNoCommand && action == nil {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}

	if mode == cfg.RunModeScanner {
		scannerName := selectedScanner(parser)
		option.Timeout = 3600
		scannerRest, err := applyScannerCommandArgs("", rest, &option)
		if err != nil {
			return parsedCLI{Option: option, Mode: mode}, err
		}
		scannerArgs := append([]string{scannerName}, scannerRest...)
		return parsedCLI{Option: option, Mode: mode, ScannerArgs: scannerArgs}, nil
	}

	if mode == runModeWeb {
		return parsedCLI{Option: option, Mode: runModeWeb, WebOpts: cli.Web}, nil
	}

	return parsedCLI{Option: option, Mode: mode, Action: action}, nil
}

func parseScannerCLI(scannerName string, rootArgs, scannerRest []string, stdout io.Writer) (parsedCLI, error) {
	var manual cfg.Option
	filteredRootArgs, err := applyScannerCommandArgs("", rootArgs, &manual)
	if err != nil {
		return parsedCLI{Option: manual, Mode: cfg.RunModeScanner, ScannerArgs: []string{scannerName}}, err
	}
	var cli cliOptions
	parser := newCLIParser(&cli, goflags.Default&^goflags.PrintErrors)
	if scannerName == "scan" {
		parser = newCLIParser(&cli, (goflags.Default&^goflags.PrintErrors)|goflags.IgnoreUnknown)
	}
	_, parseErr := parser.ParseArgs(filteredRootArgs)
	if parseErr != nil {
		err := parseErr
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			writeHelp(parser, stdout)
			return parsedCLI{Mode: cfg.RunModeNoCommand, Help: true}, nil
		}
	}

	option := cfg.Option{MiscOptions: cli.MiscOptions}
	finalizeOptions(&option, nil)
	mergeManualScannerOptions(&option, manual)
	if parseErr != nil {
		return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: []string{scannerName}}, parseErr
	}
	cfg.CaptureExplicitFlags(&option, parser)
	for flag := range manual.Explicit {
		option.MarkExplicit(flag)
	}
	if cli.Version {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}
	option.Timeout = cli.Timeout
	if option.Timeout == 0 && !option.Explicit["timeout"] {
		option.Timeout = 3600
	}

	var scannerArgs []string
	if scannerName == "scan" {
		scannerArgs, err = applyScannerCommandArgs(scannerName, scannerRest, &option)
		if err != nil {
			return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: []string{scannerName}}, err
		}
	} else {
		scannerArgs = append([]string(nil), scannerRest...)
	}
	if scannerBoolFlagEnabled(scannerArgs, "--debug") {
		option.Debug = true
	}
	if err := cfg.ResolveOutputFormat(&option); err != nil {
		return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: []string{scannerName}}, err
	}
	return parsedCLI{
		Option:      option,
		Mode:        cfg.RunModeScanner,
		ScannerArgs: append([]string{scannerName}, scannerArgs...),
	}, nil
}

func mergeManualScannerOptions(option *cfg.Option, manual cfg.Option) {
	option.OutputFile = cfg.ResolveString(manual.OutputFile, option.OutputFile)
	option.OutputFormat = cfg.ResolveString(manual.OutputFormat, option.OutputFormat)
	option.Observe = cfg.ResolveString(manual.Observe, option.Observe)
	option.JSON = option.JSON || manual.JSON
	option.ActiveProfile = cfg.ResolveString(manual.ActiveProfile, option.ActiveProfile)
	option.Provider = cfg.ResolveString(manual.Provider, option.Provider)
	option.BaseURL = cfg.ResolveString(manual.BaseURL, option.BaseURL)
	option.APIKey = cfg.ResolveString(manual.APIKey, option.APIKey)
	option.Model = cfg.ResolveString(manual.Model, option.Model)
	if manual.MaxTokens != 0 {
		option.MaxTokens = manual.MaxTokens
	}
	if manual.ContextWindow != 0 {
		option.ContextWindow = manual.ContextWindow
	}
	option.LLMProxy = cfg.ResolveString(manual.LLMProxy, option.LLMProxy)
	if manual.AI {
		option.AI = true
	}
	for key, fields := range manual.Extensions {
		if option.Extensions == nil {
			option.Extensions = cfg.Values{}
		}
		target := option.Extensions[key]
		if target == nil {
			target = map[string]any{}
			option.Extensions[key] = target
		}
		for name, value := range fields {
			target[name] = value
		}
	}
	if manual.NoColor {
		option.NoColor = true
	}
	option.Prompt = cfg.ResolveString(manual.Prompt, option.Prompt)
	option.TaskFile = cfg.ResolveString(manual.TaskFile, option.TaskFile)
	option.Resume = cfg.ResolveString(manual.Resume, option.Resume)
	if len(manual.Skills) > 0 {
		option.Skills = append(option.Skills, manual.Skills...)
	}
}

func buildOption(cli *cliOptions, parser *goflags.Parser) cfg.Option {
	var opt cfg.Option
	opt.Command = selectedMode(parser)
	opt.MiscOptions = cli.MiscOptions

	active := parser.Active
	if active == nil {
		return opt
	}

	switch active.Name {
	case "agent":
		opt.LLMOptions = cli.Agent.LLMOptions
		opt.AgentOptions = cli.Agent.AgentOptions
		opt.NodeOptions = cli.Agent.NodeOptions
	case "inbox":
		opt.LLMOptions = cli.Inbox.LLMOptions
		opt.AgentOptions = cli.Inbox.AgentOptions
		opt.NodeOptions = cli.Inbox.NodeOptions
		opt.InboxOptions = cli.InboxOptions
	case "web":
		opt.LLMOptions = cli.Web.LLMOptions
		opt.NodeOptions = cli.Web.NodeOptions
	}

	return opt
}

func newCLIParser(cli *cliOptions, options goflags.Options) *goflags.Parser {
	parser := goflags.NewParser(cli, options)
	configuration.RegisterHelp(parser)
	for _, name := range productScannerNames() {
		if _, err := parser.AddCommand(name, productScannerDescription(name), "", &struct{}{}); err != nil {
			panic(err)
		}
	}
	cli.registry = hostcli.New(parser)
	declareResources(cli.registry, &cli.Agent.AgentOptions)
	for _, group := range sessionext.FlagGroups(&cli.Inbox.AgentOptions) {
		if err := cli.registry.Group("inbox", "", group); err != nil {
			panic(err)
		}
	}
	parser.Find("inbox").FindOptionByLongName("timeout").Default = []string{"0"}
	if err := cli.registry.Group("inbox", "", cfg.FlagGroup{Name: "Inbox Options", Options: &cli.InboxOptions}); err != nil {
		panic(err)
	}
	if err := cli.registry.Group("inbox", ioaclient.ConfigKey, ioaclient.FlagGroup()); err != nil {
		panic(err)
	}
	if err := cli.registry.Group("inbox", scannerext.CyberhubConfigKey, scannerext.CyberhubFlagGroup()); err != nil {
		panic(err)
	}
	if err := cli.registry.Seal(); err != nil {
		panic(err)
	}
	parser.SubcommandsOptional = true
	parser.Usage = fmt.Sprintf(`[OPTIONS] <command>

aiscan - AI-assisted security scanner

Commands:
  init           Initialize user configuration (--project for this directory)
  config         Inspect, validate and manage configuration
  doctor         Check configuration and dependencies
  scan           Scan a target, with optional AI skills (--verify, --sniper)
  agent          Run the natural-language agent
  inbox          Run the persistent message router and subagent host
  web            Start the web UI server (includes embedded agent server)
  serve          Run the standalone agent server

Advanced scanners:
%s

Examples:
  aiscan scan -i 127.0.0.1
  aiscan scan -i http://target.example --verify=on --sniper --model gpt-4o
  aiscan agent -p "find web services and check vulnerabilities" -i 192.168.1.0/24
  aiscan inbox --state-dir .cyber/active --max-active 4
  aiscan web --addr 0.0.0.0:8080
  aiscan serve --token mykey --addr 0.0.0.0:8765`, strings.Join(productScannerUsageLines(), "\n"))
	return parser
}

func parserOptionsForArgs(args []string) goflags.Options {
	options := goflags.Options(goflags.Default &^ goflags.PrintErrors)
	if len(args) == 0 {
		return options
	}
	if productScannerAvailable(firstCommandName(args, rootFlagValueArity)) {
		options |= goflags.IgnoreUnknown
	}
	return options
}

func splitScannerCommand(args []string) (string, []string, []string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if productScannerAvailable(arg) {
			return arg, append([]string(nil), args[:i]...), append([]string(nil), args[i+1:]...), true
		}
		if shouldSkipRootFlagValue(arg) && i+1 < len(args) {
			i++
		}
	}
	return "", nil, nil, false
}

func shouldSkipRootFlagValue(arg string) bool {
	key, _, hasValue := strings.Cut(arg, "=")
	if hasValue {
		return false
	}
	return rootFlagValueArity[key] > 0
}

func firstCommandName(args []string, valueArity map[string]int) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			key, _, hasValue := strings.Cut(arg, "=")
			if !hasValue {
				i += valueArity[key]
			}
			continue
		}
		return arg
	}
	return ""
}

type knownFlag struct {
	names []string
	arity int
	// extension flags carry their own presence in Option.Extensions; they are
	// not marked in Option.Explicit.
	extension bool
	apply     func(opt *cfg.Option, val string)
}

// setExtension records a scanner-passthrough flag as a CLI-layer extension
// value; section Environment hooks read CLI presence from there.
func setExtension(o *cfg.Option, key, field string, value any) {
	if o.Extensions == nil {
		o.Extensions = cfg.Values{}
	}
	fields := o.Extensions[key]
	if fields == nil {
		fields = map[string]any{}
		o.Extensions[key] = fields
	}
	fields[field] = value
}

var scannerKnownFlags = []knownFlag{
	{names: []string{"--config", "-c"}, arity: 1, apply: func(o *cfg.Option, v string) { o.ConfigFile = v }},
	{names: []string{"--data-dir"}, arity: 1, apply: func(o *cfg.Option, v string) { o.DataDir = v }},
	{names: []string{"--cyberhub-url"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, scannerext.CyberhubConfigKey, "url", v)
	}},
	{names: []string{"--cyberhub-key"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, scannerext.CyberhubConfigKey, "key", v)
	}},
	{names: []string{"--cyberhub-mode"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, scannerext.CyberhubConfigKey, "mode", v)
	}},
	{names: []string{"--no-color"}, arity: 0, apply: func(o *cfg.Option, _ string) { o.NoColor = true }},
	{names: []string{"--ai"}, arity: 0, apply: func(o *cfg.Option, v string) {
		if v != "" {
			o.AI = truthyFlagValue(v)
		} else {
			o.AI = true
		}
	}},
	{names: []string{"--prompt", "-p"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Prompt = v }},
	{names: []string{"--task-file"}, arity: 1, apply: func(o *cfg.Option, v string) { o.TaskFile = v }},
	{names: []string{"--skill", "-s"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Skills = append(o.Skills, v) }},
	{names: []string{"--profile"}, arity: 1, apply: func(o *cfg.Option, v string) { o.ActiveProfile = v }},
	{names: []string{"--provider"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Provider = v }},
	{names: []string{"--base-url"}, arity: 1, apply: func(o *cfg.Option, v string) { o.BaseURL = v }},
	{names: []string{"--api-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.APIKey = v }},
	{names: []string{"--model"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Model = v }},
	{names: []string{"--max-tokens"}, arity: 1, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil {
			o.MaxTokens = n
		}
	}},
	{names: []string{"--context-window"}, arity: 1, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil {
			o.ContextWindow = n
		}
	}},
	{names: []string{"--proxy"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, scannerext.CyberhubConfigKey, "proxy", v)
	}},
	{names: []string{"--llm-proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.LLMProxy = v }},
	{names: []string{"--fofa-key"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, passiveconfig.ReconConfigKey, "fofa_key", v)
	}},
	{names: []string{"--hunter-api-key"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, passiveconfig.ReconConfigKey, "hunter_api_key", v)
	}},
	{names: []string{"--tavily-key"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, passiveconfig.ReconConfigKey, "tavily_key", v)
	}},
	{names: []string{"--recon-proxy"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		setExtension(o, passiveconfig.ReconConfigKey, "proxy", v)
	}},
	{names: []string{"--recon-limit"}, arity: 1, extension: true, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil {
			setExtension(o, passiveconfig.ReconConfigKey, "limit", n)
		}
	}},
	{names: []string{"--heartbeat"}, arity: 1, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil && n >= 0 {
			o.Heartbeat = n
		}
	}},
	{names: []string{"--resume"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Resume = v }},
	{names: []string{"-r"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Resume = v }},
	{names: []string{"--file", "-f"}, arity: 1, apply: func(o *cfg.Option, v string) { o.OutputFile = v }},
	{names: []string{"--output-format", "-o"}, arity: 1, apply: func(o *cfg.Option, v string) { o.OutputFormat = v }},
	{names: []string{"--json"}, arity: 0, apply: func(o *cfg.Option, _ string) { o.JSON = true }},
	{names: []string{"--observe"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Observe = v }},
}

var rootOnlyFlagValueArity = map[string]int{
	"--input":         1,
	"-i":              1,
	"--view":          1,
	"-F":              1,
	"-o":              1,
	"--output-format": 1,
	"--observe":       1,
	"--file":          1,
	"-f":              1,
	"--timeout":       1,
}

var rootFlagValueArity = buildRootFlagValueArity()

func buildRootFlagValueArity() map[string]int {
	var cli cliOptions
	_ = newCLIParser(&cli, 0)
	m := cli.registry.ValueArity()
	for _, f := range scannerKnownFlags {
		for _, name := range f.names {
			m[name] = f.arity
		}
	}
	for name, arity := range rootOnlyFlagValueArity {
		m[name] = arity
	}
	return m
}

func argsAfterCommand(args []string, command string) []string {
	for i, arg := range args {
		if arg == command {
			return append([]string(nil), args[i+1:]...)
		}
	}
	return nil
}

func selectedMode(parser *goflags.Parser) cfg.RunMode {
	active := parser.Active
	if active == nil {
		return cfg.RunModeNoCommand
	}
	switch active.Name {
	case "agent":
		return cfg.RunModeAgent
	case "inbox":
		return cfg.RunModeInbox
	case "web":
		return runModeWeb
	default:
		if productScannerAvailable(active.Name) {
			return cfg.RunModeScanner
		}
	}
	return cfg.RunModeNoCommand
}

func selectedScanner(parser *goflags.Parser) string {
	active := parser.Active
	if active == nil {
		return ""
	}
	if productScannerAvailable(active.Name) {
		return active.Name
	}
	return ""
}

func applyScannerCommandArgs(scannerName string, args []string, option *cfg.Option) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(arg, "=")
		matched := false
		for _, f := range scannerKnownFlags {
			if !slices.Contains(f.names, key) {
				continue
			}
			// scan owns --ai and --json as native scanner flags. Root forms
			// before the command remain Cyber options; forms after the command
			// must reach the scan command unchanged.
			if scannerName == "scan" && (key == "--ai" || key == "--json") {
				break
			}
			matched = true
			if !f.extension {
				option.MarkExplicit(f.names[0])
			}
			if f.arity == 0 {
				if hasValue {
					f.apply(option, value)
				} else {
					f.apply(option, "")
				}
			} else {
				v, err := flagValue(arg, hasValue, value, args, &i)
				if err != nil {
					return nil, err
				}
				f.apply(option, v)
			}
			break
		}
		if !matched {
			out = append(out, arg)
		}
	}
	return out, nil
}

func flagValue(arg string, hasValue bool, value string, args []string, i *int) (string, error) {
	if hasValue {
		return value, nil
	}
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s requires a value", arg)
	}
	*i++
	return args[*i], nil
}

func truthyFlagValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

type signalHandler struct {
	mu     sync.Mutex
	stopFn func() bool
}

func (h *signalHandler) SetStopFunc(fn func() bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopFn = fn
}

func (h *signalHandler) tryStop() bool {
	h.mu.Lock()
	fn := h.stopFn
	h.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return false
}

func setupSignalHandler(cancel context.CancelFunc, logger telemetry.Logger) *signalHandler {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	handler := &signalHandler{}
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sigCount := 0
		var lastSig time.Time
		for range sigChan {
			now := time.Now()
			if now.Sub(lastSig) > 5*time.Second {
				sigCount = 0
			}
			sigCount++
			lastSig = now

			switch sigCount {
			case 1:
				if handler.tryStop() {
					sigCount = 0
					continue
				}
				fmt.Fprintf(os.Stderr, "\nPress Ctrl+C again to exit\n")
			case 2:
				logger.Warnf("signal=shutdown action=force_exit")
				cancel()
				os.Exit(130)
			default:
				logger.Warnf("signal=shutdown action=force_exit")
				os.Exit(1)
			}
		}
	}()
	return handler
}

func writeHelp(parser *goflags.Parser, writer io.Writer) {
	if parser.Active == nil {
		parser.WriteHelp(writer)
		return
	}

	// Parser.Usage contains the long root command catalog. go-flags reuses it
	// verbatim when rendering subcommand help, which pushes the active command's
	// flags below the fold. Keep the detailed catalog for `aiscan -h`, but use a
	// compact root prefix for `aiscan <command> -h`.
	rootUsage := parser.Usage
	parser.Usage = "[GLOBAL OPTIONS]"
	defer func() { parser.Usage = rootUsage }()
	parser.WriteHelp(writer)
}
