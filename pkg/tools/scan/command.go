package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	toolargs.Base
	engines     *engine.Set
	parent      *agent.Agent
	deepBrowser DeepBrowserFunc
	readSkill   SkillReader
}

type flags struct {
	Inputs          []string `short:"i" long:"input" description:"Input target: URL, IP, IP:port, or CIDR"`
	ListFile        string   `short:"l" long:"list" description:"File containing input targets, one per line"`
	Mode            string   `long:"mode" description:"Scan profile: quick or full" default:"quick"`
	Thread          int      `long:"thread" description:"Total concurrency budget distributed across engines" default:"1000"`
	Sniper          bool     `long:"sniper" description:"Use AI to search public vulnerabilities for discovered fingerprints"`
	Deep            bool     `long:"deep" description:"Run deep AI testing for discovered websites and fingerprinted assets"`
	Trace           bool     `long:"trace" description:"Show internal scanner source and pipeline trace"`
	Debug           bool     `long:"debug" description:"Enable trace and underlying scanner debug logs"`
	JSON            bool     `short:"j" long:"json" description:"Output raw gogo and spray results as JSON Lines"`
	Report          bool     `long:"report" description:"Output a concise final markdown report"`
	OutputFile      string   `short:"f" long:"file" description:"Write output to file without ANSI colors"`
	AssetReportFile string   `short:"F" long:"format" description:"Write aggregated asset report to file"`
	NoColor         bool     `long:"no-color" description:"Disable ANSI colors in terminal output"`
	Ports           string   `long:"ports" description:"Ports for gogo scanning; defaults to all in quick and - in full"`
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
	Verify          string   `long:"verify" description:"Use AI to verify loots at priority threshold: auto, off, low, medium, high, or critical"`
}

func New(engineSet *engine.Set, opts ...Option) *Command {
	cmd := &Command{engines: engineSet}
	cmd.InitLogger(nil)
	for _, opt := range opts {
		if opt != nil {
			opt(cmd)
		}
	}
	return cmd
}

func (c *Command) InitLogger(logger telemetry.Logger) {
	c.Base.InitLogger(logger)
	if c.parent != nil {
		c.parent.Cfg.Logger = c.Logger
	}
}

func (c *Command) Name() string { return "scan" }

func (c *Command) Usage() string {
	return Usage()
}

func Usage() string {
	var options flags
	return toolargs.GoFlagsHelp("scan", &options)
}

func (c *Command) Execute(ctx context.Context, args []string) (err error) {
	defer telemetry.RecoverAsError("scan", &err)
	out, result, err := c.execute(ctx, c.resolveRelativePaths(args), nil)
	if err != nil {
		return err
	}
	output.PublishResult(ctx, result)
	if out != "" {
		fmt.Fprint(commands.Output, out)
	}
	return nil
}

func (c *Command) ExecuteStructured(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error) {
	out, result, err := c.execute(ctx, c.resolveRelativePaths(args), stream)
	if err == nil {
		output.PublishResult(ctx, result)
	}
	return out, result, err
}

func (c *Command) execute(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error) {
	var flags flags
	parser := toolargs.NewGoFlagsParser("scan", &flags)
	if _, err := parser.ParseArgs(args); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return c.Usage() + "\n", nil, nil
		}
		return "", nil, fmt.Errorf("scan: %w", err)
	}
	if flags.Debug {
		flags.Trace = true
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("scan debug enabled")
	}
	profile, err := profileForMode(flags.Mode)
	if err != nil {
		return "", nil, fmt.Errorf("scan: %w", err)
	}
	var verifyLevel priority
	if flags.Verify != "" && flags.Verify != "off" {
		vl, err := parsePriority(flags.Verify)
		if err != nil {
			return "", nil, fmt.Errorf("scan: %w", err)
		}
		verifyLevel = vl
	}
	options := resolveScanOptions(flags)

	rawInputs, err := readInputs(flags.Inputs, flags.ListFile)
	if err != nil {
		return "", nil, err
	}
	if len(rawInputs) == 0 {
		if flags.AssetReportFile != "" {
			return output.RenderRecordFileAsAsset(flags.AssetReportFile, !flags.NoColor, AggregateStructuredResult)
		}
		return "", nil, fmt.Errorf("scan: no input targets")
	}

	if flags.JSON || flags.Report {
		stream = nil
	}

	trace := flags.Trace || flags.Debug
	pipelineBus := eventbus.New[pipeline.Observation]()
	coll := newCollector(rawInputs, stream, stream != nil && !flags.NoColor, trace)
	subscribePipeline(pipelineBus, coll, trace)

	var scanWriter *scanJSONLWriter
	if flags.OutputFile != "" {
		var agentBus *eventbus.Bus[agent.Event]
		if c.parent != nil {
			agentBus = c.parent.Cfg.Bus
		}
		w, wErr := newScanJSONLWriter(flags.OutputFile, pipelineBus, agentBus)
		if wErr != nil {
			return "", nil, fmt.Errorf("scan: open record file: %w", wErr)
		}
		scanWriter = w
		defer scanWriter.Close()
		scanWriter.WriteRecord(output.NewRecord(output.TypeScanStart, output.ScanStart{
			Targets: rawInputs, Mode: flags.Mode, Flags: args,
		}))
	}

	seeds := buildSeedEvents(rawInputs, func(raw string) {
		pipelineBus.Emit(pipeline.Observation{
			Action: pipeline.ActionAccept,
			Event:  errorEventOf("", fmt.Sprintf("skip invalid input: %s", raw)),
		})
	})
	if len(seeds) == 0 {
		return "", nil, fmt.Errorf("scan: no valid inputs")
	}

	capabilities := c.buildCapabilities(flags, options, profile)
	p, err := pipeline.New(ctx, pipeline.Config{
		Capabilities: capabilities,
		Bus:          pipelineBus,
	})
	if err != nil {
		return "", nil, fmt.Errorf("scan: %w", err)
	}
	p.Run(seedsToEvents(seeds))

	if c.parent != nil && verifyLevel != "" {
		runVerifyPass(ctx, c.parent, c.readSkill, coll, verifyLevel, c.Logger)
	}
	if c.parent != nil && flags.Sniper {
		runSniperPass(ctx, c.parent, c.readSkill, coll, c.Logger)
	}

	coll.Finish()

	var out string
	if flags.JSON {
		out, err = coll.JSONLines()
		if err != nil {
			return "", nil, fmt.Errorf("scan json output: %w", err)
		}
	} else if flags.Report {
		out = coll.ReportMarkdown()
	} else {
		out = coll.TerminalString(stream != nil && !flags.NoColor)
	}
	if scanWriter != nil {
		coll.mu.Lock()
		stats := coll.statsSnapshotLocked()
		gogoCount := len(coll.gogoResults)
		webCount := len(coll.seenWeb)
		lootCount := len(coll.loots)
		errCount := len(coll.errors)
		coll.mu.Unlock()
		scanWriter.WriteRecord(output.NewRecord(output.TypeScanEnd, output.ScanEnd{
			Duration: stats.Duration().Seconds(),
			Targets:  stats.Inputs,
			Services: gogoCount,
			Webs:     webCount,
			Loots:    lootCount,
			Errors:   errCount,
		}))
	}
	if flags.OutputFile != "" && !flags.JSON {
		plainOut := coll.PlainText()
		if err := writeOutputFile(flags.OutputFile, plainOut); err != nil {
			c.Logger.Errorf("%s", err.Error())
		}
	}
	if flags.AssetReportFile != "" {
		assetOut := coll.AssetReport()
		if err := writeOutputFile(flags.AssetReportFile, assetOut); err != nil {
			c.Logger.Errorf("%s", err.Error())
		}
	}
	return out, coll.StructuredResult(), nil
}

var scanFileFlags = map[string]bool{
	"-l": true, "--list": true,
	"-f": true, "--file": true,
	"-F": true, "--format": true,
	"--dict": true, "--rule": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, scanFileFlags, c.WorkDir)
}

func writeOutputFile(path, content string) error {
	path = filepath.Clean(path)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("scan output file: create directory: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("scan output file: %w", err)
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return fmt.Errorf("scan output file: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("scan output file: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("scan output file: close: %w", err)
	}
	return nil
}
