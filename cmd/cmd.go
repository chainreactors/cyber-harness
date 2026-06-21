package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webagent"
	goflags "github.com/jessevdk/go-flags"
)

type cliOptions struct {
	cfg.Option
	Agent struct{}   `command:"agent" description:"Run the LLM agent"`
	IOA   ioaCommand `command:"ioa" description:"IOA server commands"`
	cfg.ScannerCommands
}

type ioaCommand struct {
	Serve    struct{}       `command:"serve" description:"Run the IOA HTTP server"`
	Spaces   struct{}       `command:"spaces" description:"List all IOA spaces"`
	Messages ioaMessagesCmd `command:"messages" description:"List start messages in a space"`
	Context  ioaContextCmd  `command:"context" description:"View message thread/context"`
	Nodes    ioaNodesCmd    `command:"nodes" description:"List nodes"`
}

type ioaMessagesCmd struct {
	Positional struct {
		Space string `positional-arg-name:"space"`
	} `positional-args:"yes" required:"yes"`
}

type ioaContextCmd struct {
	Positional struct {
		Space     string `positional-arg-name:"space"`
		MessageID string `positional-arg-name:"message-id"`
	} `positional-args:"yes" required:"yes"`
}

type ioaNodesCmd struct {
	Positional struct {
		Space string `positional-arg-name:"space"`
	} `positional-args:"yes"`
}

type parsedCLI struct {
	Option      cfg.Option
	Mode        cfg.RunMode
	ScannerArgs []string
	IOAArgs     cfg.IOAClientArgs
	Help        bool
}

func AiScan() {
	parsed, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	option := parsed.Option
	if option.Version {
		fmt.Printf("aiscan v%s\n", cfg.Version)
		return
	}
	if option.InitConfig {
		if err := os.WriteFile(cfg.DefaultConfigName, []byte(cfg.InitDefaultConfig()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Config file generated: %s\n", cfg.DefaultConfigName)
		return
	}
	if option.ViewFile != "" {
		if err := output.RenderFile(option.ViewFile, option.ViewFormat, option.ViewOutput); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		return
	}
	if parsed.Help {
		return
	}
	if parsed.Mode == cfg.RunModeNoCommand {
		fmt.Fprintf(os.Stderr, "error: missing subcommand: use %s\n", cfg.CLICommandSummary())
		os.Exit(1)
	}

	cfgPath, err := cfg.ResolveRuntimeConfig(&option)
	if err != nil {
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
	if parsed.Mode == cfg.RunModeIOAServe {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(option.Timeout)*time.Second)
	}
	defer cancel()

	sigHandler := setupSignalHandler(cancel, logger)

	switch parsed.Mode {
	case cfg.RunModeAgent:
		var err error
		if option.WebURL != "" {
			err = webagent.Run(ctx, &option, logger)
		} else {
			err = runner.RunAgentMode(ctx, &option, logger, sigHandler.SetStopFunc)
		}
		if err != nil {
			logger.Errorf("agent failed: %s", err)
			os.Exit(1)
		}
	case cfg.RunModeIOAServe:
		if err := runner.RunIOAServe(ctx, &option, logger); err != nil {
			logger.Errorf("ioa server failed: %s", err)
			os.Exit(1)
		}
	case cfg.RunModeIOASpaces, cfg.RunModeIOAMessages, cfg.RunModeIOAContext, cfg.RunModeIOANodes:
		if err := runner.RunIOAClientCommand(ctx, parsed.Mode, &option, parsed.IOAArgs, logger); err != nil {
			logger.Errorf("ioa command failed: %s", err)
			os.Exit(1)
		}
	case cfg.RunModeScanner:
		if err := runner.RunDirectScannerMode(ctx, &option, parsed.ScannerArgs, logger); err != nil {
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
				option := cli.Option
				option.Timeout = 3600
				scannerArgs := append([]string{scannerName}, argsAfterCommand(args, scannerName)...)
				return parsedCLI{Option: option, Mode: cfg.RunModeScanner, ScannerArgs: scannerArgs}, nil
			}
			printHelp(parser)
			return parsedCLI{Mode: cfg.RunModeNoCommand, Help: true}, nil
		}
		return parsedCLI{}, err
	}

	option := cli.Option
	if cli.Version {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}

	mode := selectedMode(parser)
	if mode == cfg.RunModeNoCommand {
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

	ioaArgs := extractIOAArgs(&cli, mode)
	return parsedCLI{Option: option, Mode: mode, IOAArgs: ioaArgs}, nil
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

	option := cli.Option
	mergeManualScannerOptions(&option, manual)
	if cli.Version {
		return parsedCLI{Option: option, Mode: cfg.RunModeNoCommand}, nil
	}
	option.Timeout = 3600

	scannerArgs := append([]string(nil), scannerRest...)
	if scannerName == "scan" {
		scannerArgs, err = applyScannerCommandArgs(scannerName, scannerRest, &option)
		if err != nil {
			return parsedCLI{}, err
		}
	}
	if boolFlagEnabled(scannerArgs, "--debug") {
		option.Debug = true
	}
	return parsedCLI{
		Option:      option,
		Mode:        cfg.RunModeScanner,
		ScannerArgs: append([]string{scannerName}, scannerArgs...),
	}, nil
}

func mergeManualScannerOptions(option *cfg.Option, manual cfg.Option) {
	option.Provider = cfg.ResolveString(manual.Provider, option.Provider)
	option.BaseURL = cfg.ResolveString(manual.BaseURL, option.BaseURL)
	option.APIKey = cfg.ResolveString(manual.APIKey, option.APIKey)
	option.Model = cfg.ResolveString(manual.Model, option.Model)
	option.LLMProxy = cfg.ResolveString(manual.LLMProxy, option.LLMProxy)
	if manual.AI {
		option.AI = true
	}
	option.CyberhubURL = cfg.ResolveString(manual.CyberhubURL, option.CyberhubURL)
	option.CyberhubKey = cfg.ResolveString(manual.CyberhubKey, option.CyberhubKey)
	option.CyberhubMode = cfg.ResolveString(manual.CyberhubMode, option.CyberhubMode)
	option.FofaEmail = cfg.ResolveString(manual.FofaEmail, option.FofaEmail)
	option.FofaKey = cfg.ResolveString(manual.FofaKey, option.FofaKey)
	option.HunterToken = cfg.ResolveString(manual.HunterToken, option.HunterToken)
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
	option.WebURL = cfg.ResolveString(manual.WebURL, option.WebURL)
	if len(manual.Skills) > 0 {
		option.Skills = append(option.Skills, manual.Skills...)
	}
}

func newCLIParser(cli *cliOptions, options goflags.Options) *goflags.Parser {
	parser := goflags.NewParser(cli, options)
	parser.SubcommandsOptional = true
	parser.Usage = fmt.Sprintf(`[OPTIONS] <command>

aiscan - AI-assisted security scanner

Commands:
  scan           Scan a target, with optional AI skills (--verify, --sniper, --deep)
  agent          Run the natural-language agent

Advanced scanners:
%s

Infrastructure:
  cyberhub       Search Cyberhub fingerprints and POCs
  ioa serve      Run the IOA HTTP server
  ioa spaces     List all IOA spaces
  ioa messages   List start messages in a space
  ioa context    View message thread/context
  ioa nodes      List nodes

Examples:
  aiscan scan -i 127.0.0.1
  aiscan scan -i http://target.com --verify=high --sniper --model gpt-4o
  aiscan scan -i http://target.com --sniper
  aiscan scan -i http://target.com --mode full --deep
  aiscan scan -i 192.168.1.0/24 --mode full
  aiscan scan -i http://target.com --mode full --verify=high --sniper --report
  aiscan agent -p "find web services and check vulnerabilities" -i 192.168.1.0/24
  aiscan ioa serve
  aiscan ioa serve --ioa-token mysecret
  aiscan ioa spaces --ioa-url http://token@127.0.0.1:8765
  aiscan ioa messages default --ioa-url http://token@127.0.0.1:8765
  aiscan agent --web-url http://127.0.0.1:8080
  aiscan agent --web-url http://127.0.0.1:8080 --ioa-url http://token@127.0.0.1:8080/ioa --space case-1`, cfg.ScannerUsageLines())
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
	{names: []string{"--web-url"}, arity: 1, apply: func(o *cfg.Option, v string) { o.WebURL = v }},
	{names: []string{"--skill", "-s"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Skills = append(o.Skills, v) }},
	{names: []string{"--provider"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Provider = v }},
	{names: []string{"--base-url"}, arity: 1, apply: func(o *cfg.Option, v string) { o.BaseURL = v }},
	{names: []string{"--api-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.APIKey = v }},
	{names: []string{"--model"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Model = v }},
	{names: []string{"--proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.Proxy = v }},
	{names: []string{"--llm-proxy"}, arity: 1, apply: func(o *cfg.Option, v string) { o.LLMProxy = v }},
	{names: []string{"--fofa-email"}, arity: 1, apply: func(o *cfg.Option, v string) { o.FofaEmail = v }},
	{names: []string{"--fofa-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.FofaKey = v }},
	{names: []string{"--hunter-token"}, arity: 1, apply: func(o *cfg.Option, v string) { o.HunterToken = v }},
	{names: []string{"--hunter-api-key"}, arity: 1, apply: func(o *cfg.Option, v string) { o.HunterAPIKey = v }},
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
}

var rootOnlyFlagValueArity = map[string]int{
	"--input":  1,
	"-i":       1,
	"--view":   1,
	"-F":       1,
	"--output": 1,
	"-o":       1,
	"--file":   1,
	"-f":       1,
}

var rootFlagValueArity = buildRootFlagValueArity()

func buildRootFlagValueArity() map[string]int {
	m := make(map[string]int, len(scannerKnownFlags)*2)
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
	return cfg.ScannerCommandAvailable(name)
}

func selectedMode(parser *goflags.Parser) cfg.RunMode {
	active := parser.Active
	if active == nil {
		return cfg.RunModeNoCommand
	}
	if active.Name == "ioa" && active.Active != nil {
		switch active.Active.Name {
		case "serve":
			return cfg.RunModeIOAServe
		case "spaces":
			return cfg.RunModeIOASpaces
		case "messages":
			return cfg.RunModeIOAMessages
		case "context":
			return cfg.RunModeIOAContext
		case "nodes":
			return cfg.RunModeIOANodes
		}
	}
	switch active.Name {
	case "agent":
		return cfg.RunModeAgent
	case "serve":
		return cfg.RunModeIOAServe
	default:
		if cfg.ScannerCommandAvailable(active.Name) {
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
	if cfg.ScannerCommandAvailable(active.Name) {
		return active.Name
	}
	return ""
}

func extractIOAArgs(cli *cliOptions, mode cfg.RunMode) cfg.IOAClientArgs {
	switch mode {
	case cfg.RunModeIOAMessages:
		return cfg.IOAClientArgs{Space: cli.IOA.Messages.Positional.Space}
	case cfg.RunModeIOAContext:
		return cfg.IOAClientArgs{
			Space:     cli.IOA.Context.Positional.Space,
			MessageID: cli.IOA.Context.Positional.MessageID,
		}
	case cfg.RunModeIOANodes:
		return cfg.IOAClientArgs{Space: cli.IOA.Nodes.Positional.Space}
	}
	return cfg.IOAClientArgs{}
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
			if !containsString(f.names, key) {
				continue
			}
			if scannerName == "scan" && key == "--ai" {
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

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
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

// SignalHandler manages SIGINT/SIGTERM with a two-phase shutdown:
// first Ctrl+C stops the current task (if any), second cancels the
// root context, third force-exits.
type SignalHandler struct {
	mu     sync.Mutex
	stopFn func() bool
}

// SetStopFunc registers a callback that attempts to stop the current
// task. It should return true if a task was stopped.
func (h *SignalHandler) SetStopFunc(fn func() bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopFn = fn
}

func (h *SignalHandler) tryStop() bool {
	h.mu.Lock()
	fn := h.stopFn
	h.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return false
}

func setupSignalHandler(cancel context.CancelFunc, logger telemetry.Logger) *SignalHandler {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	handler := &SignalHandler{}
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
				logger.Warnf("signal=shutdown action=finish_current_turn")
				cancel()
			default:
				logger.Warnf("signal=shutdown action=force_exit")
				os.Exit(1)
			}
		}
	}()
	return handler
}

func printHelp(parser *goflags.Parser) {
	parser.WriteHelp(os.Stdout)
}
