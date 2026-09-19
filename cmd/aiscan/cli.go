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

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/output"
	"github.com/chainreactors/cyber/core/telemetry"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	"github.com/chainreactors/cyber/pkg/runner"
	goflags "github.com/jessevdk/go-flags"
)

const runModeWeb cfg.RunMode = "web"

func cliCommandSummary() string {
	base := "agent, web, serve"
	summaries := scannerext.Names()
	if len(summaries) == 0 {
		return base
	}
	return base + ", " + strings.Join(summaries, ", ")
}

type webCommand struct {
	Addr               string `long:"addr" default:"127.0.0.1:8080" description:"HTTP listen address"`
	DB                 string `long:"db" default:"cyber-web.db" description:"SQLite database path"`
	MaxScans           int    `long:"max-scans" default:"3" description:"Maximum concurrent scans"`
	ScanTimeout        int    `long:"scan-timeout" default:"600" description:"Maximum scan runtime in seconds"`
	Token              string `long:"token" description:"Access key for the server (auto-generated if empty)"`
	NoAgent            bool   `long:"no-agent" description:"Start the web console only, without the embedded agent node"`
	cfg.LLMOptions     `group:"LLM Options"`
	cfg.ScannerOptions `group:"Scanner Options"`
	cfg.NodeOptions    `group:"Server Options"`
	cfg.ReconOptions   `group:"Recon Options"`
}

type cliOptions struct {
	registry        *hostcli.Registry `no-flag:"true"`
	cfg.MiscOptions `group:"Miscellaneous Options"`
	Timeout         int          `long:"timeout" description:"Overall timeout in seconds"`
	Agent           agentCommand `command:"agent" description:"Run the natural-language agent"`
	Web             webCommand   `command:"web" description:"Start the web UI server (includes embedded agent server)"`
}

type agentCommand struct {
	cfg.LLMOptions     `group:"LLM Options"`
	cfg.ScannerOptions `group:"Scanner Options"`
	cfg.AgentOptions   `no-flag:"true"`
	cfg.NodeOptions    `group:"Server Options"`
	cfg.ReconOptions   `group:"Recon Options"`
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
	parsed, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	option := parsed.Option
	explicitOption := option
	if option.Version {
		fmt.Printf("aiscan v%s\n", cfg.Version)
		return
	}
	if option.InitConfig {
		if err := os.WriteFile(cfg.DefaultConfigName, []byte(defaultConfig()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Config file generated: %s\n", cfg.DefaultConfigName)
		return
	}
	if option.ViewFile != "" {
		if err := output.RenderEventFile(option.ViewFile, option.ViewFormat, option.ViewOutput); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		return
	}
	if parsed.Help {
		return
	}
	if parsed.Mode == cfg.RunModeNoCommand && parsed.Action == nil {
		fmt.Fprintf(os.Stderr, "error: missing subcommand: use %s\n", cliCommandSummary())
		os.Exit(1)
	}

	cfgPath, err := runner.ResolveRuntimeConfig(&option)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	if err := applyIdentity(&option); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	if cfgPath != "" && option.Debug {
		fmt.Fprintf(os.Stderr, "loaded config: %s\n", cfgPath)
	}
	if cfgPath != "" {
		option.ConfigFile = cfgPath
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Debug: option.Debug, Quiet: option.Quiet, Output: os.Stderr, Color: !option.NoColor})

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	switch {
	case parsed.Mode == runModeWeb || parsed.Action != nil && parsed.Action.Persistent:
		ctx, cancel = context.WithCancel(context.Background())
	default:
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(option.Timeout)*time.Second)
	}
	defer cancel()

	sigHandler := setupSignalHandler(cancel, logger)
	if parsed.Action != nil {
		if err := parsed.Action.Run(ctx, hostcli.Environment{Config: &option, Logger: logger, Out: os.Stdout, Err: os.Stderr}); err != nil {
			logger.Errorf("command failed: %s", err)
			os.Exit(1)
		}
		return
	}

	switch parsed.Mode {
	case cfg.RunModeAgent:
		err := runAgentTransport(ctx, newCyberProfileFromRequest, &option, logger, os.Stdin, os.Stdout, sigHandler.SetStopFunc)
		if err != nil {
			logger.Errorf("agent failed: %s", err)
			os.Exit(1)
		}
	case runModeWeb:
		if err := serveWeb(ctx, &option, &explicitOption, parsed.WebOpts, logger); err != nil {
			logger.Errorf("web server failed: %s", err)
			os.Exit(1)
		}
	case cfg.RunModeScanner:
		if err := runner.RunDirectScannerMode(ctx, newCyberProfileFromRequest, &option, parsed.ScannerArgs, logger); err != nil {
			logger.Errorf("scanner command failed: %s", err)
			os.Exit(1)
		}
	}
}

func parseCLI(args []string) (parsedCLI, error) {
	if scannerName, rootArgs, scannerRest, ok := splitScannerCommand(args); ok {
		return parseScannerCLI(scannerName, rootArgs, scannerRest)
	}

	var cli cliOptions
	parser := newCLIParser(&cli, parserOptionsForArgs(args))
	rest, err := parser.ParseArgs(args)
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			if scannerName := firstCommandName(args, rootFlagValueArity); isScannerCommandName(scannerName) {
				option := cfg.Option{MiscOptions: cli.MiscOptions}
				finalizeOptions(&option, nil)
				option.Timeout = 3600
				scannerArgs := append([]string{scannerName}, argsAfterCommand(args, scannerName)...)
				return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: scannerArgs}, nil
			}
			printHelp(parser)
			return parsedCLI{Mode: cfg.RunModeNoCommand, Help: true}, nil
		}
		return parsedCLI{}, err
	}

	if cli.Version {
		return parsedCLI{Option: cfg.Option{MiscOptions: cli.MiscOptions}, Mode: cfg.RunModeNoCommand}, nil
	}

	mode := selectedMode(parser)
	option := buildOption(&cli, parser)
	action := cli.registry.Selected()
	option.Extensions = cli.registry.Values()
	finalizeOptions(&option, action)
	if cli.Timeout > 0 {
		option.Timeout = cli.Timeout
	}
	if option.Timeout <= 0 {
		// Commands that own their options through the extension registry (the IOA
		// queries, for one) never receive AgentOptions, so nothing else supplies
		// this default. A zero deadline would cancel the context before the
		// command runs.
		option.Timeout = 3600
	}
	if err := validateOutputFlags(&option); err != nil {
		return parsedCLI{}, err
	}

	if mode == cfg.RunModeNoCommand && action == nil {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}

	if mode == cfg.RunModeScanner {
		scannerName := selectedScanner(parser)
		option.Timeout = 3600
		scannerRest, err := applyScannerRootArgs(rest, &option)
		if err != nil {
			return parsedCLI{}, err
		}
		scannerArgs := append([]string{scannerName}, scannerRest...)
		return parsedCLI{Option: option, Mode: mode, ScannerArgs: scannerArgs}, nil
	}

	if mode == runModeWeb {
		return parsedCLI{Option: option, Mode: runModeWeb, WebOpts: cli.Web}, nil
	}

	return parsedCLI{Option: option, Mode: mode, Action: action}, nil
}

func parseScannerCLI(scannerName string, rootArgs, scannerRest []string) (parsedCLI, error) {
	var manual cfg.Option
	filteredRootArgs, err := applyScannerCommandArgs("", rootArgs, &manual)
	if err != nil {
		return parsedCLI{}, err
	}
	var cli cliOptions
	parser := newCLIParser(&cli, goflags.Default&^goflags.PrintErrors)
	if scannerName == "scan" {
		parser = newCLIParser(&cli, (goflags.Default&^goflags.PrintErrors)|goflags.IgnoreUnknown)
	}
	if _, err := parser.ParseArgs(filteredRootArgs); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			printHelp(parser)
			return parsedCLI{Mode: cfg.RunModeNoCommand, Help: true}, nil
		}
		return parsedCLI{}, err
	}

	option := cfg.Option{MiscOptions: cli.MiscOptions}
	finalizeOptions(&option, nil)
	mergeManualScannerOptions(&option, manual)
	if cli.Version {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}
	option.Timeout = cli.Timeout
	if option.Timeout <= 0 {
		option.Timeout = 3600
	}

	var scannerArgs []string
	if scannerName == "scan" {
		scannerArgs, err = applyScannerCommandArgs(scannerName, scannerRest, &option)
		if err != nil {
			return parsedCLI{}, err
		}
	} else {
		scannerArgs = append([]string(nil), scannerRest...)
	}
	if boolFlagEnabled(scannerArgs, "--debug") {
		option.Debug = true
	}
	if err := validateOutputFlags(&option); err != nil {
		return parsedCLI{}, err
	}
	return parsedCLI{
		Option:      option,
		Mode:        cfg.RunModeScanner,
		ScannerArgs: append([]string{scannerName}, scannerArgs...),
	}, nil
}

func validateOutputFlags(option *cfg.Option) error {
	format := strings.TrimSpace(option.OutputFormat)
	if option.JSON {
		format = "json"
	}
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" && format != "stream-json" {
		return fmt.Errorf("unsupported --output-format %q: use text, json, or stream-json", format)
	}
	if strings.TrimSpace(option.ViewOutput) != "" && strings.TrimSpace(option.ViewFile) == "" {
		return fmt.Errorf("--file/-f is only valid with --view/-F")
	}
	option.OutputFormat = format
	return nil
}

func mergeManualScannerOptions(option *cfg.Option, manual cfg.Option) {
	option.OutputFile = cfg.ResolveString(manual.OutputFile, option.OutputFile)
	option.OutputFormat = cfg.ResolveString(manual.OutputFormat, option.OutputFormat)
	option.Observe = cfg.ResolveString(manual.Observe, option.Observe)
	option.JSON = option.JSON || manual.JSON
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
	option.CyberhubURL = cfg.ResolveString(manual.CyberhubURL, option.CyberhubURL)
	option.CyberhubKey = cfg.ResolveString(manual.CyberhubKey, option.CyberhubKey)
	option.CyberhubMode = cfg.ResolveString(manual.CyberhubMode, option.CyberhubMode)
	option.FofaKey = cfg.ResolveString(manual.FofaKey, option.FofaKey)
	option.HunterAPIKey = cfg.ResolveString(manual.HunterAPIKey, option.HunterAPIKey)
	option.ReconProxy = cfg.ResolveString(manual.ReconProxy, option.ReconProxy)
	if manual.ReconLimit != nil {
		option.ReconLimit = manual.ReconLimit
	}
	option.Proxy = cfg.ResolveString(manual.Proxy, option.Proxy)
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
	opt.MiscOptions = cli.MiscOptions

	active := parser.Active
	if active == nil {
		return opt
	}

	switch active.Name {
	case "agent":
		opt.LLMOptions = cli.Agent.LLMOptions
		opt.ScannerOptions = cli.Agent.ScannerOptions
		opt.AgentOptions = cli.Agent.AgentOptions
		opt.NodeOptions = cli.Agent.NodeOptions
		opt.ReconOptions = cli.Agent.ReconOptions
	case "web":
		opt.LLMOptions = cli.Web.LLMOptions
		opt.ScannerOptions = cli.Web.ScannerOptions
		opt.NodeOptions = cli.Web.NodeOptions
		opt.ReconOptions = cli.Web.ReconOptions
	}

	return opt
}

func newCLIParser(cli *cliOptions, options goflags.Options) *goflags.Parser {
	parser := goflags.NewParser(cli, options)
	for _, name := range scannerext.Names() {
		if _, err := parser.AddCommand(name, scannerext.Description(name), "", &struct{}{}); err != nil {
			panic(err)
		}
	}
	cli.registry = hostcli.New(parser)
	declareResources(cli.registry, &cli.Agent.AgentOptions)
	if err := cli.registry.Seal(); err != nil {
		panic(err)
	}
	parser.SubcommandsOptional = true
	parser.Usage = fmt.Sprintf(`[OPTIONS] <command>

aiscan - AI-assisted security scanner

Commands:
  scan           Scan a target, with optional AI skills (--verify, --sniper, --deep)
  agent          Run the natural-language agent
  web            Start the web UI server (includes embedded agent server)
  serve          Run the standalone agent server

Advanced scanners:
%s

Examples:
  aiscan scan -i 127.0.0.1
  aiscan scan -i http://target.com --verify=high --sniper --model gpt-4o
  aiscan agent -p "find web services and check vulnerabilities" -i 192.168.1.0/24
  aiscan web --addr 0.0.0.0:8080
  aiscan serve --token mykey --addr 0.0.0.0:8765`, strings.Join(scannerext.UsageLines(), "\n"))
	return parser
}

func parserOptionsForArgs(args []string) goflags.Options {
	options := goflags.Options(goflags.Default &^ goflags.PrintErrors)
	if len(args) == 0 {
		return options
	}
	if isScannerCommandName(firstCommandName(args, rootFlagValueArity)) {
		options |= goflags.IgnoreUnknown
	}
	return options
}

func splitScannerCommand(args []string) (string, []string, []string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isScannerCommandName(arg) {
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
	apply func(opt *cfg.Option, val string)
}

var scannerKnownFlags = []knownFlag{
	{names: []string{"--config", "-c"}, arity: 1, apply: func(o *cfg.Option, v string) { o.ConfigFile = v }},
	{names: []string{"--data-dir"}, arity: 1, apply: func(o *cfg.Option, v string) { o.DataDir = v }},
	{names: []string{"--cyberhub-url"}, arity: 1, apply: func(o *cfg.Option, v string) { o.CyberhubURL = v }},
	{names: []string{"--cyberhub-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.CyberhubKey = v }},
	{names: []string{"--cyberhub-mode"}, arity: 1, apply: func(o *cfg.Option, v string) { o.CyberhubMode = v }},
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
	{names: []string{"--proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Proxy = v }},
	{names: []string{"--llm-proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.LLMProxy = v }},
	{names: []string{"--fofa-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.FofaKey = v }},
	{names: []string{"--hunter-api-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.HunterAPIKey = v }},
	{names: []string{"--tavily-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.TavilyKey = v }},
	{names: []string{"--recon-proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.ReconProxy = v }},
	{names: []string{"--recon-limit"}, arity: 1, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil {
			o.ReconLimit = &n
		}
	}},
	{names: []string{"--heartbeat"}, arity: 1, apply: func(o *cfg.Option, v string) {
		if n, e := strconv.Atoi(v); e == nil && n >= 0 {
			o.Heartbeat = n
		}
	}},
	{names: []string{"--resume"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Resume = v }},
	{names: []string{"-r"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Resume = v }},
	{names: []string{"--output", "-o"}, arity: 1, apply: func(o *cfg.Option, v string) { o.OutputFile = v }},
	{names: []string{"--output-format"}, arity: 1, apply: func(o *cfg.Option, v string) { o.OutputFormat = v }},
	{names: []string{"--json"}, arity: 0, apply: func(o *cfg.Option, _ string) { o.JSON = true }},
	{names: []string{"--observe"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Observe = v }},
}

var rootOnlyFlagValueArity = map[string]int{
	"--input":         1,
	"-i":              1,
	"--view":          1,
	"-F":              1,
	"--output":        1,
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

func isScannerCommandName(name string) bool {
	return scannerext.Available(name)
}

func selectedMode(parser *goflags.Parser) cfg.RunMode {
	active := parser.Active
	if active == nil {
		return cfg.RunModeNoCommand
	}
	switch active.Name {
	case "agent":
		return cfg.RunModeAgent
	case "web":
		return runModeWeb
	default:
		if scannerext.Available(active.Name) {
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
	if scannerext.Available(active.Name) {
		return active.Name
	}
	return ""
}

func applyScannerRootArgs(args []string, option *cfg.Option) ([]string, error) {
	return applyScannerCommandArgs("", args, option)
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

func boolFlagEnabled(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
		if strings.HasPrefix(arg, flag+"=") {
			v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, flag+"=")))
			return v != "false" && v != "0" && v != "no"
		}
	}
	return false
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

func printHelp(parser *goflags.Parser) {
	writeHelp(parser, os.Stdout)
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
