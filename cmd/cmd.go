package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/aiscan/skills"
	goflags "github.com/jessevdk/go-flags"
)

const Version = "0.1.0"

type Option struct {
	LLMOptions     `group:"LLM Options" config:"llm"`
	ScannerOptions `group:"Scanner Options" config:"cyberhub"`
	AgentOptions   `group:"Agent Options" config:"agent"`
	IOAOptions     `group:"IOA Options" config:"ioa"`
	ReconOptions   `group:"Recon Options" config:"recon"`
	MiscOptions    `group:"Miscellaneous Options" config:"misc"`
	ScanConfig scanConfigOptions `no-flag:"true" config:"scan"`
}

type scanConfigOptions struct {
	Verify        string `config:"verify"`
	VerifyTimeout int    `config:"verify_timeout"`
}

type LLMOptions struct {
	Provider string `long:"provider" config:"provider" description:"LLM provider name (openai, deepseek, openrouter, ollama, etc.)"`
	BaseURL  string `long:"base-url" config:"base_url" description:"LLM API base URL"`
	APIKey   string `long:"api-key" config:"api_key" description:"LLM API key (or set env: OPENAI_API_KEY, AISCAN_API_KEY)"`
	Model    string `long:"model" config:"model" description:"LLM model name"`
	LLMProxy string `long:"llm-proxy" config:"proxy" description:"Proxy for LLM API requests"`
	AI       bool   `long:"ai" description:"Enable all AI skills: verify findings, sniper fingerprint analysis, and summarize results"`
}

type ScannerOptions struct {
	CyberhubURL  string `long:"cyberhub-url" config:"url" description:"Cyberhub server URL for loading fingers/templates"`
	CyberhubKey  string `long:"cyberhub-key" config:"key" description:"Cyberhub API key"`
	CyberhubMode string `long:"cyberhub-mode" config:"mode" description:"Cyberhub resource mode: merge or override"`
	Proxy        string `long:"proxy" config:"proxy" description:"Proxy for scanner tools (gogo, spray, zombie, neutron), e.g. socks5://127.0.0.1:1080"`
}

type AgentOptions struct {
	Prompt     string   `short:"p" long:"prompt" description:"Natural language task for the agent"`
	Inputs     []string `short:"i" long:"input" description:"Target input: IP, URL, IP:port, or CIDR. Can specify multiple"`
	Skills     []string `short:"s" long:"skill" description:"Embedded skill to apply. Can specify multiple"`
	TaskFile   string   `long:"task-file" description:"File containing task description"`
	Loop       bool     `long:"loop" description:"Run as an IOA loop worker instead of local agent mode"`
	Heartbeat  int      `long:"heartbeat" description:"Run an IOA heartbeat agent turn every N minutes in agent --loop (0 disables)" default:"0"`
	Timeout    int      `long:"timeout" config:"timeout" description:"Overall timeout in seconds" default:"3600"`
	EventsFile string   `long:"events-file" description:"Write agent events to JSONL file"`
}

type IOAOptions struct {
	IOAURL      string `long:"ioa-url" config:"url" description:"IOA server URL for agent tools"`
	IOANodeID   string `long:"ioa-node-id" description:"Existing IOA node id for agent tools"`
	IOANodeName string `long:"ioa-node-name" config:"node_name" description:"IOA node name when auto-registering"`
	Space       string `long:"space" config:"space" description:"IOA space name for 'aiscan agent --loop'" default:"default"`
	IOAJSON     bool   `long:"json" description:"Output IOA query results in JSON format"`
}

type MiscOptions struct {
	ConfigFile string `short:"c" long:"config" description:"Path to config file (default: ./config.yaml, ~/.config/aiscan/config.yaml)"`
	InitConfig bool   `long:"init" description:"Generate default config.yaml and exit"`
	ViewFile   string `short:"F" long:"view" description:"View a scan record JSONL file"`
	ViewFormat string `short:"o" long:"output" description:"Output format for -F: terminal (default), markdown" default:"terminal"`
	ViewOutput string `short:"f" long:"file" description:"Write -F output to file instead of stdout"`
	Debug      bool   `long:"debug" config:"debug" description:"Enable debug logging"`
	Quiet      bool   `short:"q" long:"quiet" config:"quiet" description:"Quiet mode"`
	NoColor    bool   `long:"no-color" config:"no_color" description:"Disable ANSI colors in scanner output"`
	Version    bool   `long:"version" description:"Print version and exit"`
}

type cliOptions struct {
	Option
	Agent struct{}   `command:"agent" description:"Run the LLM agent"`
	IOA   ioaCommand `command:"ioa" description:"IOA server commands"`
	scannerCommands
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

type runMode string

const (
	runModeAgent       runMode = "agent"
	runModeIOAServe    runMode = "ioa serve"
	runModeIOASpaces   runMode = "ioa spaces"
	runModeIOAMessages runMode = "ioa messages"
	runModeIOAContext  runMode = "ioa context"
	runModeIOANodes    runMode = "ioa nodes"
	runModeScanner     runMode = "scanner"
	runModeNoCommand   runMode = ""
)

type ioaClientArgs struct {
	Space     string
	MessageID string
}

type parsedCLI struct {
	Option      Option
	Mode        runMode
	ScannerArgs []string
	IOAArgs     ioaClientArgs
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
		fmt.Printf("aiscan v%s\n", Version)
		return
	}
	if option.InitConfig {
		if err := os.WriteFile(defaultConfigName, []byte(InitDefaultConfig()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Config file generated: %s\n", defaultConfigName)
		return
	}
	if option.ViewFile != "" {
		if err := runViewFile(option.ViewFile, option.ViewFormat, option.ViewOutput); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		return
	}
	if parsed.Help {
		return
	}
	if parsed.Mode == runModeNoCommand {
		fmt.Fprintf(os.Stderr, "error: missing subcommand: use %s\n", cliCommandSummary())
		os.Exit(1)
	}

	cfgPath, err := loadAndApplyConfig(&option)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	if cfgPath != "" && option.Debug {
		fmt.Fprintf(os.Stderr, "loaded config: %s\n", cfgPath)
	}
	applyDefaults(&option)
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Debug: option.Debug, Quiet: option.Quiet, Output: os.Stderr, Color: !option.NoColor})

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if parsed.Mode == runModeIOAServe {
		// ioa serve is a long-running daemon; do not inherit the agent timeout.
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(option.Timeout)*time.Second)
	}
	defer cancel()

	setupSignalHandler(cancel, logger)

	switch parsed.Mode {
	case runModeAgent:
		if err := runAgentMode(ctx, &option, logger); err != nil {
			logger.Errorf("agent failed: %s", err)
			os.Exit(1)
		}
	case runModeIOAServe:
		if err := runIOAServe(ctx, &option, logger); err != nil {
			logger.Errorf("ioa server failed: %s", err)
			os.Exit(1)
		}
	case runModeIOASpaces, runModeIOAMessages, runModeIOAContext, runModeIOANodes:
		if err := runIOAClientCommand(ctx, parsed.Mode, &option, parsed.IOAArgs, logger); err != nil {
			logger.Errorf("ioa command failed: %s", err)
			os.Exit(1)
		}
	case runModeScanner:
		if err := runDirectScannerMode(ctx, &option, parsed.ScannerArgs, logger); err != nil {
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
				return parsedCLI{Option: option, Mode: runModeScanner, ScannerArgs: scannerArgs}, nil
			}
			printHelp(parser)
			return parsedCLI{Mode: runModeNoCommand, Help: true}, nil
		}
		return parsedCLI{}, err
	}

	option := cli.Option
	if cli.Version {
		return parsedCLI{Option: option, Mode: runModeNoCommand}, nil
	}

	mode := selectedMode(parser)
	if mode == runModeNoCommand {
		return parsedCLI{Option: option, Mode: runModeNoCommand}, nil
	}

	if mode == runModeScanner {
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
	var manual Option
	filteredRootArgs, err := applyScannerCommandArgs(scannerName, rootArgs, &manual)
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
			return parsedCLI{Mode: runModeNoCommand, Help: true}, nil
		}
		return parsedCLI{}, err
	}

	option := cli.Option
	mergeManualScannerOptions(&option, manual)
	if cli.Version {
		return parsedCLI{Option: option, Mode: runModeNoCommand}, nil
	}
	option.Timeout = 3600

	scannerArgs := append([]string(nil), scannerRest...)
	if scannerName == "scan" {
		scannerArgs, err = applyScannerCommandArgs(scannerName, scannerRest, &option)
		if err != nil {
			return parsedCLI{}, err
		}
	}
	if toolargs.BoolFlagEnabled(scannerArgs, "--debug") {
		option.Debug = true
	}
	return parsedCLI{
		Option:      option,
		Mode:        runModeScanner,
		ScannerArgs: append([]string{scannerName}, scannerArgs...),
	}, nil
}

func mergeManualScannerOptions(option *Option, manual Option) {
	option.Provider = resolveString(manual.Provider, option.Provider)
	option.BaseURL = resolveString(manual.BaseURL, option.BaseURL)
	option.APIKey = resolveString(manual.APIKey, option.APIKey)
	option.Model = resolveString(manual.Model, option.Model)
	option.LLMProxy = resolveString(manual.LLMProxy, option.LLMProxy)
	if manual.AI {
		option.AI = true
	}
	option.CyberhubURL = resolveString(manual.CyberhubURL, option.CyberhubURL)
	option.CyberhubKey = resolveString(manual.CyberhubKey, option.CyberhubKey)
	option.CyberhubMode = resolveString(manual.CyberhubMode, option.CyberhubMode)
	mergeReconOptions(option, &manual)
	option.ScannerOptions.Proxy = resolveString(manual.ScannerOptions.Proxy, option.ScannerOptions.Proxy)
	if manual.NoColor {
		option.NoColor = true
	}
	option.Prompt = resolveString(manual.Prompt, option.Prompt)
	option.TaskFile = resolveString(manual.TaskFile, option.TaskFile)
	if len(manual.Skills) > 0 {
		option.Skills = append(option.Skills, manual.Skills...)
	}
}

func mergeReconOptions(option, manual *Option) {
	option.FofaEmail = resolveString(manual.FofaEmail, option.FofaEmail)
	option.FofaKey = resolveString(manual.FofaKey, option.FofaKey)
	option.HunterToken = resolveString(manual.HunterToken, option.HunterToken)
	option.HunterAPIKey = resolveString(manual.HunterAPIKey, option.HunterAPIKey)
	option.ReconProxy = resolveString(manual.ReconProxy, option.ReconProxy)
	if manual.ReconLimit != nil {
		option.ReconLimit = manual.ReconLimit
	}
}

func newCLIParser(cli *cliOptions, options goflags.Options) *goflags.Parser {
	parser := goflags.NewParser(cli, options)
	parser.SubcommandsOptional = true
	parser.Usage = fmt.Sprintf(`[OPTIONS] <command>

aiscan - AI-assisted security scanner

Commands:
  scan           Scan a target, with optional AI skills (--ai, --sniper)
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
  aiscan scan -i http://target.com --ai --model gpt-4o
  aiscan scan -i http://target.com --sniper
  aiscan scan -i 192.168.1.0/24 --mode full
  aiscan scan -i http://target.com --mode full --ai --report
  aiscan agent -p "find web services and check vulnerabilities" -i 192.168.1.0/24
  aiscan ioa serve
  aiscan ioa spaces --ioa-url http://127.0.0.1:8765
  aiscan ioa messages default --ioa-url http://127.0.0.1:8765
  aiscan agent --loop -p "localhost web scanner" -s aiscan --space case-1
  aiscan agent --loop --heartbeat 5 --space case-1 -p "coordinate next scan steps"`, scannerUsageLines())
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
	arity int // 0 for bool, 1 for value
	apply func(opt *Option, val string)
}

var scannerKnownFlags = []knownFlag{
	{names: []string{"--config", "-c"}, arity: 1, apply: func(o *Option, v string) { o.ConfigFile = v }},
	{names: []string{"--cyberhub-url"}, arity: 1, apply: func(o *Option, v string) { o.CyberhubURL = v }},
	{names: []string{"--cyberhub-key"}, arity: 1, apply: func(o *Option, v string) { o.CyberhubKey = v }},
	{names: []string{"--cyberhub-mode"}, arity: 1, apply: func(o *Option, v string) { o.CyberhubMode = v }},
	{names: []string{"--no-color"}, arity: 0, apply: func(o *Option, _ string) { o.NoColor = true }},
	{names: []string{"--ai"}, arity: 0, apply: func(o *Option, v string) {
		if v != "" {
			o.AI = truthyFlagValue(v)
		} else {
			o.AI = true
		}
	}},
	{names: []string{"--prompt", "-p"}, arity: 1, apply: func(o *Option, v string) { o.Prompt = v }},
	{names: []string{"--task-file"}, arity: 1, apply: func(o *Option, v string) { o.TaskFile = v }},
	{names: []string{"--skill", "-s"}, arity: 1, apply: func(o *Option, v string) { o.Skills = append(o.Skills, v) }},
	{names: []string{"--provider"}, arity: 1, apply: func(o *Option, v string) { o.Provider = v }},
	{names: []string{"--base-url"}, arity: 1, apply: func(o *Option, v string) { o.BaseURL = v }},
	{names: []string{"--api-key"}, arity: 1, apply: func(o *Option, v string) { o.APIKey = v }},
	{names: []string{"--model"}, arity: 1, apply: func(o *Option, v string) { o.Model = v }},
	{names: []string{"--proxy"}, arity: 1, apply: func(o *Option, v string) { o.ScannerOptions.Proxy = v }},
	{names: []string{"--llm-proxy"}, arity: 1, apply: func(o *Option, v string) { o.LLMProxy = v }},
	{names: []string{"--fofa-email"}, arity: 1, apply: func(o *Option, v string) { o.FofaEmail = v }},
	{names: []string{"--fofa-key"}, arity: 1, apply: func(o *Option, v string) { o.FofaKey = v }},
	{names: []string{"--hunter-token"}, arity: 1, apply: func(o *Option, v string) { o.HunterToken = v }},
	{names: []string{"--hunter-api-key"}, arity: 1, apply: func(o *Option, v string) { o.HunterAPIKey = v }},
	{names: []string{"--recon-proxy"}, arity: 1, apply: func(o *Option, v string) { o.ReconProxy = v }},
	{names: []string{"--recon-limit"}, arity: 1, apply: func(o *Option, v string) {
		if n, e := strconv.Atoi(v); e == nil {
			o.ReconLimit = &n
		}
	}},
	{names: []string{"--heartbeat"}, arity: 1, apply: func(o *Option, v string) {
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
	return scannerCommandAvailable(name)
}

func selectedMode(parser *goflags.Parser) runMode {
	active := parser.Active
	if active == nil {
		return runModeNoCommand
	}
	if active.Name == "ioa" && active.Active != nil {
		switch active.Active.Name {
		case "serve":
			return runModeIOAServe
		case "spaces":
			return runModeIOASpaces
		case "messages":
			return runModeIOAMessages
		case "context":
			return runModeIOAContext
		case "nodes":
			return runModeIOANodes
		}
	}
	switch active.Name {
	case "agent":
		return runModeAgent
	case "serve":
		return runModeIOAServe
	default:
		if scannerCommandAvailable(active.Name) {
			return runModeScanner
		}
	}
	return runModeNoCommand
}

func selectedScanner(parser *goflags.Parser) string {
	active := parser.Active
	if active == nil {
		return ""
	}
	if scannerCommandAvailable(active.Name) {
		return active.Name
	}
	return ""
}

func extractIOAArgs(cli *cliOptions, mode runMode) ioaClientArgs {
	switch mode {
	case runModeIOAMessages:
		return ioaClientArgs{Space: cli.IOA.Messages.Positional.Space}
	case runModeIOAContext:
		return ioaClientArgs{
			Space:     cli.IOA.Context.Positional.Space,
			MessageID: cli.IOA.Context.Positional.MessageID,
		}
	case runModeIOANodes:
		return ioaClientArgs{Space: cli.IOA.Nodes.Positional.Space}
	}
	return ioaClientArgs{}
}

func applyScannerRootArgs(args []string, option *Option) ([]string, error) {
	return applyScannerCommandArgs("", args, option)
}

func applyScannerCommandArgs(_ string, args []string, option *Option) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(arg, "=")
		matched := false
		for _, f := range scannerKnownFlags {
			if !containsString(f.names, key) {
				continue
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

func hasAgentOneShotInput(opt *Option) bool {
	if strings.TrimSpace(opt.Prompt) != "" || opt.TaskFile != "" || len(opt.Inputs) > 0 {
		return true
	}
	return !stdinIsTerminal()
}

func stdinIsTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func resolveTask(opt *Option) (string, error) {
	prompt := strings.TrimSpace(opt.Prompt)
	if prompt != "" {
		if len(opt.Inputs) > 0 {
			return fmt.Sprintf("%s\n\nTargets:\n%s", prompt, formatInputs(opt.Inputs)), nil
		}
		return prompt, nil
	}

	if opt.TaskFile != "" {
		data, err := os.ReadFile(opt.TaskFile)
		if err != nil {
			return "", fmt.Errorf("read task file: %w", err)
		}
		task := strings.TrimSpace(string(data))
		if len(opt.Inputs) > 0 {
			return fmt.Sprintf("%s\n\nTargets:\n%s", task, formatInputs(opt.Inputs)), nil
		}
		return task, nil
	}

	if !stdinIsTerminal() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		task := strings.TrimSpace(string(data))
		if task != "" {
			if len(opt.Inputs) > 0 {
				return fmt.Sprintf("%s\n\nTargets:\n%s", task, formatInputs(opt.Inputs)), nil
			}
			return task, nil
		}
	}

	if len(opt.Inputs) > 0 {
		return fmt.Sprintf("Scan the provided targets using scan and summarize findings.\n\nTargets:\n%s", formatInputs(opt.Inputs)), nil
	}

	return "", fmt.Errorf("no prompt specified: use -p, --prompt, --task-file, or pipe via stdin")
}

func isDirectScannerJSONOutput(rest []string) bool {
	if !isDirectScannerCommand(rest) {
		return false
	}

	for _, arg := range rest[1:] {
		if arg == "-j" || arg == "--json" {
			return true
		}
		if strings.HasPrefix(arg, "--json=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--json=")))
			return value != "false" && value != "0" && value != "no"
		}
	}
	return false
}

func isDirectScannerCommand(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	return scannerCommandAvailable(rest[0])
}

func shouldStreamScannerOutput(rest []string) bool {
	if len(rest) == 0 || rest[0] != "scan" {
		return false
	}
	if isDirectScannerJSONOutput(rest) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "--report" {
			return false
		}
		if strings.HasPrefix(arg, "--report=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--report=")))
			if value != "false" && value != "0" && value != "no" {
				return false
			}
		}
	}
	return true
}

func hasScannerFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func applySelectedSkills(text string, selected []string, store *skills.Store) (string, error) {
	if len(selected) == 0 {
		return text, nil
	}
	var sb strings.Builder
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		skill, ok := store.ByName(name)
		if !ok {
			return "", fmt.Errorf("unknown skill %q", name)
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(skills.FormatInvocation(skill, ""))
	}
	if strings.TrimSpace(text) != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(strings.TrimSpace(text))
	}
	return sb.String(), nil
}

func formatInputs(inputs []string) string {
	var sb strings.Builder
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(input)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func setupSignalHandler(cancel context.CancelFunc, logger telemetry.Logger) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sigCount := 0
		for range sigChan {
			sigCount++
			if sigCount == 1 {
				logger.Warnf("signal=shutdown action=finish_current_turn")
				cancel()
			} else {
				logger.Warnf("signal=shutdown action=force_exit")
				os.Exit(1)
			}
		}
	}()
}

func printHelp(parser *goflags.Parser) {
	parser.WriteHelp(os.Stdout)
}
