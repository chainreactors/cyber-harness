package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	engines      *engine.Set
	verification VerificationConfig
	verifyFunc   VerifyFunc
	logger       telemetry.Logger
}

type flags struct {
	Inputs          []string `short:"i" long:"input" description:"Input target: URL, IP, IP:port, or CIDR"`
	ListFile        string   `short:"l" long:"list" description:"File containing input targets, one per line"`
	Mode            string   `long:"mode" description:"Scan profile: quick or full" default:"quick"`
	Thread          int      `long:"thread" description:"Total concurrency budget distributed across engines" default:"1000"`
	Debug           bool     `long:"debug" description:"Print event pipeline trace to stderr"`
	JSON            bool     `short:"j" long:"json" description:"Output raw gogo and spray results as JSON Lines"`
	Report          bool     `long:"report" description:"Output a concise final markdown report"`
	OutputFile      string   `short:"f" long:"file" description:"Write output to file without ANSI colors"`
	NoColor         bool     `long:"no-color" description:"Disable ANSI colors in terminal output"`
	Ports           string   `long:"ports" description:"Ports for gogo scanning; defaults to all in quick and - in full"`
	Port            string   `long:"port" description:"Ports for discovery scanning; overrides --ports when set"`
	Threads         int // derived from Thread; not a CLI flag
	Timeout         int `long:"timeout" description:"Per-probe timeout in seconds" default:"5"`
	SprayThreads    int // derived from Thread; not a CLI flag
	Dictionaries    []string `long:"dict" description:"Dictionary file for spray word-based discovery. Can specify multiple."`
	Rules           []string `long:"rule" description:"Rule file for spray word mutation. Can specify multiple."`
	Word            string   `long:"word" description:"Spray word-generation DSL"`
	DefaultDict     bool     `long:"default-dict" description:"Use spray default dictionary for word-based discovery"`
	Advance         bool     `long:"advance" description:"Enable spray advance plugin behavior for enabled web capabilities"`
	ZombieThreads   int // derived from Thread; not a CLI flag
	ZombieTop       int      `long:"zombie-top" description:"Use top N default weakpass words"`
	Users           []string `long:"user" description:"Weakpass usernames. Can specify multiple."`
	Passwords       []string `long:"pwd" description:"Weakpass passwords. Can specify multiple."`
	MaxNeutronPerFP int      `long:"max-neutron-per-finger" description:"Maximum neutron templates per fingerprint" default:"20"`
	BroadPOC        bool     `long:"broad-poc" description:"Run neutron POC checks even when no fingerprint matched"`
	Verify          string   `long:"verify" description:"LLM verification mode: off, low, medium, high, critical" default:"off"`
	VerifyTimeout   int      `long:"verify-timeout" description:"Timeout in seconds per verification" default:"120"`
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
	return `scan - Capability-driven automatic scan pipeline
Usage: scan -i <target> [options]
Inputs:
  -i, --input       URL, IP, IP:port, or CIDR. Can specify multiple.
  -l, --list        File containing inputs, one per line. CIDR is allowed.
Options:
      --mode        Scan profile: quick or full (default: quick)
      --thread      Total concurrency budget (default: 1000); auto-distributed across engines
      --debug       Print event pipeline trace to stderr
  -j, --json        Output raw gogo and spray results as JSON Lines
      --report      Output a concise final markdown report
  -f, --file        Write output to file without ANSI colors
      --no-color    Disable ANSI colors in terminal output
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
      --broad-poc   Run neutron POC checks even when no fingerprint matched
      --verify      LLM verification mode: off, low, medium, high, critical (default: off)
      --verify-timeout  Timeout seconds per verification (default: 120)
Profiles:
  quick: gogo -p all -e -v, spray check/finger/common/crawl(depth=1)/bak/active with recon, weakpass, fingerprint-based POC
  full: quick plus gogo -p -, crawl depth=2, and spray_brute default dictionary
Flow:
  input targets -> capability queues -> emitted events -> downstream capabilities
Examples:
  scan -i 192.168.1.0/24 --mode quick
  scan -i http://target.com --mode full --debug
  scan -i 127.0.0.1 --mode quick --verify=high
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
	c.applyVerificationDefaults(&flags, args)

	profile, err := profileForMode(flags.Mode)
	if err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	if flags.BroadPOC {
		profile.AllowBroadPOC = true
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

	if flags.JSON || flags.Report {
		stream = nil
	}

	coll := newCollector(rawInputs, stream, stream != nil && !flags.NoColor, flags.Debug)
	seeds := buildSeedEvents(rawInputs, func(raw string) {
		coll.Observe(pipelineEvent{Action: pipelineEventAccept, Event: errorEventOf("", fmt.Sprintf("skip invalid input: %s", raw))})
	})
	if len(seeds) == 0 {
		return "", fmt.Errorf("scan: no valid inputs")
	}

	capabilities := c.buildCapabilities(flags, options, profile)
	observe, debugFn := wrapObserve(coll, flags.Debug)
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
	} else if flags.Report {
		out = coll.ReportMarkdown()
	} else {
		out = coll.String()
	}
	if flags.OutputFile != "" {
		fileOut := out
		if !flags.JSON && !flags.Report {
			fileOut = coll.PlainText()
		}
		if err := writeOutputFile(flags.OutputFile, fileOut); err != nil {
			return "", err
		}
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
