package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	engines    *engine.Set
	aiFunc     AIFunc
	reportFunc ReportFunc
	aiConfig   AISkillConfig
	skillStore SkillBodyLoader
	recorder   *recorder
	logger     telemetry.Logger
}

type flags struct {
	Inputs          []string `short:"i" long:"input" description:"Input target: URL, IP, IP:port, or CIDR"`
	ListFile        string   `short:"l" long:"list" description:"File containing input targets, one per line"`
	Mode            string   `long:"mode" description:"Scan profile: quick or full" default:"quick"`
	Thread          int      `long:"thread" description:"Total concurrency budget distributed across engines" default:"1000"`
	AI              bool     `long:"ai" description:"Enable all AI skills: verify findings and sniper fingerprint analysis"`
	Sniper          bool     `long:"sniper" description:"Use AI to search public vulnerabilities for discovered fingerprints"`
	Trace           bool     `long:"trace" description:"Show internal scanner source and pipeline trace"`
	Debug           bool     `long:"debug" description:"Enable trace and underlying scanner debug logs"`
	JSON            bool     `short:"j" long:"json" description:"Output raw gogo and spray results as JSON Lines"`
	Report          bool     `long:"report" description:"Output a concise final markdown report"`
	OutputFile      string   `short:"f" long:"file" description:"Write output to file without ANSI colors"`
	NoColor         bool     `long:"no-color" description:"Disable ANSI colors in terminal output"`
	Ports           string   `long:"ports" description:"Ports for gogo scanning; defaults to all in quick and - in full"`
	Port            string   `long:"port" description:"Ports for discovery scanning; overrides --ports when set"`
	Threads         int      // derived from Thread; not a CLI flag
	Timeout         int      `long:"timeout" description:"Per-probe timeout in seconds" default:"5"`
	SprayThreads    int      // derived from Thread; not a CLI flag
	Dictionaries    []string `long:"dict" description:"Dictionary file for spray word-based discovery. Can specify multiple."`
	Rules           []string `long:"rule" description:"Rule file for spray word mutation. Can specify multiple."`
	Word            string   `long:"word" description:"Spray word-generation DSL"`
	DefaultDict     bool     `long:"default-dict" description:"Use spray default dictionary for word-based discovery"`
	Advance         bool     `long:"advance" description:"Enable spray advance plugin behavior for enabled web capabilities"`
	ZombieThreads   int      // derived from Thread; not a CLI flag
	ZombieTop       int      `long:"zombie-top" description:"Use top N default weakpass words"`
	Users           []string `long:"user" description:"Weakpass usernames. Can specify multiple."`
	Passwords       []string `long:"pwd" description:"Weakpass passwords. Can specify multiple."`
	MaxNeutronPerFP int      `long:"max-neutron-per-finger" description:"Maximum neutron templates per fingerprint" default:"20"`
	BroadPOC        bool     `long:"broad-poc" description:"Run POC templates even without matching fingerprints"`
	Verify          string   `long:"verify" hidden:"true" description:"Deprecated: use --ai"`
	VerifyTimeout   int      `long:"verify-timeout" hidden:"true" description:"Deprecated advanced compatibility option" default:"120"`
}

func New(engineSet *engine.Set, opts ...Option) *Command {
	cmd := &Command{engines: engineSet, logger: telemetry.NopLogger()}
	for _, opt := range opts {
		if opt != nil {
			opt(cmd)
		}
	}
	return cmd
}

func (c *Command) Name() string { return "scan" }

func (c *Command) Usage() string {
	return Usage()
}

func Usage() string {
	return `scan - automatic security scan
Usage: scan -i <target> [options]
Inputs:
  -i, --input       URL, IP, IP:port, or CIDR. Can specify multiple.
  -l, --list        File containing inputs, one per line. CIDR is allowed.
Options:
      --mode        Scan profile: quick or full (default: quick)
      --ai          Enable all AI skills: verify + sniper (requires LLM provider)
      --sniper      Use AI to search public vulnerabilities for discovered fingerprints
      --report      Output a concise final markdown report
  -f, --file        Write output to file without ANSI colors
      --no-color    Disable ANSI colors in terminal output
      --trace       Show internal scanner source and pipeline trace
      --debug       Enable trace and underlying scanner debug logs

Advanced:
      --thread      Total concurrency budget (default: 1000); auto-distributed across engines
  -j, --json        Output raw gogo and spray results as JSON Lines
      --ports       Ports for gogo scanning (default: all in quick, - in full)
      --port        Ports for discovery scanning; overrides --ports when set
      --timeout     Timeout in seconds (default: 5)
      --dict        Dictionary file for spray word-based discovery. Can specify multiple.
      --rule        Rule file for spray word mutation. Can specify multiple.
      --word        Spray word-generation DSL
      --default-dict  Use spray default dictionary for word-based discovery
      --advance     Enable spray advance plugin behavior for enabled web capabilities
      --zombie-top        Use top N default weakpass words
      --user        Weakpass username. Can specify multiple.
      --pwd         Weakpass password. Can specify multiple.
      --max-neutron-per-finger  Maximum neutron templates per fingerprint (default: 20)
Profiles:
  quick: fast exposure discovery, web probes, HTTP Basic weakpass, and fingerprint-based POC checks
  full: deeper ports, crawl depth=2, common backup/active web checks, and default web dictionary
AI Skills:
  --ai enables all AI skills automatically. Individual skills can be used standalone:
  --sniper: search public CVEs/exploits for each fingerprint via AI agent
  --ai (verify): validate medium/high findings with LLM reasoning
Output:
  default: [web], [service], [fingerprint], [risk], [vuln], [sniper], [ai], [summary]
  --trace: also prints internal gogo/spray/zombie/neutron source and pipeline events
Examples:
  scan -i 192.168.1.0/24 --mode quick
  scan -i http://target.com --ai
  scan -i http://target.com --sniper
  scan -i http://target.com --mode full --ai --report
  scan -i 192.168.1.0/24 --port top100
  scan -i 127.0.0.1 --mode quick -j
  scan -i 127.0.0.1 --mode quick -f 1.txt
  scan -i 127.0.0.1 --mode quick --report
  scan -i 127.0.0.1 --user admin --pwd admin123
  scan -i http://target.com --dict paths.txt --rule rules.txt
  scan -l targets.txt --mode full --zombie-top 5`
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	return c.execute(ctx, args, nil)
}

func (c *Command) ExecuteStreaming(ctx context.Context, args []string, stream io.Writer) (string, error) {
	return c.execute(ctx, args, stream)
}

func (c *Command) execute(ctx context.Context, args []string, stream io.Writer) (string, error) {
	var flags flags
	parser := goflags.NewParser(&flags, goflags.Default&^goflags.PrintErrors)
	if _, err := parser.ParseArgs(args); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return c.Usage() + "\n", nil
		}
		return "", fmt.Errorf("scan: %w", err)
	}
	if flags.Debug {
		flags.Trace = true
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("scan debug enabled")
	}
	if c.aiConfig.Enable {
		flags.AI = true
	}
	if flags.AI {
		if strings.TrimSpace(flags.Verify) == "" {
			flags.Verify = "high"
		}
		flags.Sniper = true
	}

	profile, err := profileForMode(flags.Mode)
	if err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	if verificationEnabled(flags.Verify) {
		if _, err := parsePriority(flags.Verify); err != nil {
			return "", fmt.Errorf("scan: %w", err)
		}
	}
	options := resolveScanOptions(flags)

	rawInputs, err := readInputs(flags.Inputs, flags.ListFile)
	if err != nil {
		return "", err
	}
	if len(rawInputs) == 0 {
		return "", fmt.Errorf("scan: no input targets")
	}

	if flags.OutputFile != "" {
		rec, recErr := newRecorder(flags.OutputFile)
		if recErr != nil {
			return "", fmt.Errorf("scan: open record file: %w", recErr)
		}
		c.recorder = rec
		defer func() { c.recorder.Close(); c.recorder = nil }()
		c.recorder.ScanStart(rawInputs, flags.Mode, args)
	}

	if flags.JSON || flags.Report {
		stream = nil
	}

	trace := flags.Trace || flags.Debug
	coll := newCollector(rawInputs, stream, stream != nil && !flags.NoColor, trace)
	coll.recorder = c.recorder
	seeds := buildSeedEvents(rawInputs, func(raw string) {
		coll.Observe(pipelineEvent{Action: pipelineEventAccept, Event: errorEventOf("", fmt.Sprintf("skip invalid input: %s", raw))})
	})
	if len(seeds) == 0 {
		return "", fmt.Errorf("scan: no valid inputs")
	}

	capabilities := c.buildCapabilities(flags, options, profile)
	observe, debugFn := wrapObserve(coll, trace)
	p := pipeline.New(ctx, pipeline.Config{
		Capabilities: capabilities,
		Observe:      observe,
		Debug:        debugFn,
	})
	p.Run(seedsToEvents(seeds))
	coll.Finish()

	var out string
	if flags.JSON {
		out, err = coll.JSONLines()
		if err != nil {
			return "", fmt.Errorf("scan json output: %w", err)
		}
	} else if flags.Report && flags.AI && c.aiFunc != nil {
		out = c.generateAIReport(ctx, coll)
	} else if flags.Report {
		out = coll.ReportMarkdown()
	} else {
		out = coll.TerminalString(stream != nil && !flags.NoColor)
	}
	if c.recorder != nil {
		stats := coll.statsSnapshotLocked()
		c.recorder.ScanEnd(stats.Duration(), stats.Inputs,
			len(coll.gogoResults), len(coll.webEndpoints),
			len(coll.neutronMatches)+len(coll.zombieResults),
			len(coll.aiSkillResults), len(coll.errors))
	}
	return out, nil
}

func writeOutputFile(path, content string) error {
	path = filepath.Clean(path)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("scan output file: create directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("scan output file: %w", err)
	}
	return nil
}
