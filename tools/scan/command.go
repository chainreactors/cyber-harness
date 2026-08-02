package scan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/chainreactors/aiscan/tools/scan/pipeline"
	"github.com/chainreactors/aiscan/tools/toolargs"
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
	OutputFile      string   `short:"f" long:"file" description:"Write output to file without ANSI colors"`
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

func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("scan", &err)
	out, _, err := c.execute(ctx, c.resolveRelativePaths(execution.Args), execution.Stdout)
	if err != nil {
		return nil, err
	}
	if out != "" {
		fmt.Fprint(execution.Stdout, out)
	}
	// Structured scan facts are emitted through the SCO sidecar. Returning the
	// collector's private aggregation here would leak a second result schema
	// through AOP tool.result.
	return nil, nil
}

func (c *Command) execute(ctx context.Context, args []string, stream io.Writer) (string, *output.ScanResult, error) {
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
		return "", nil, fmt.Errorf("scan: no input targets")
	}

	if flags.JSON {
		stream = nil
	}

	trace := flags.Trace || flags.Debug
	pipelineBus := eventbus.New[pipeline.Observation]()
	coll := newCollector(rawInputs, stream, stream != nil && !flags.NoColor, trace)
	subscribePipeline(pipelineBus, coll, trace, stream)

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
	} else {
		out = coll.TerminalString(stream != nil && !flags.NoColor)
	}
	result := coll.StructuredResult()
	c.emitStructuredData(ctx, result)
	if flags.OutputFile != "" {
		nodes := buildSCONodes(result)
		lines := make([][]byte, 0, len(nodes))
		for _, node := range nodes {
			lines = append(lines, node)
		}
		if err := writeOutputFile(flags.OutputFile, string(bytes.Join(lines, []byte{'\n'}))); err != nil {
			c.Logger.Errorf("%s", err.Error())
		}
	}
	return out, result, nil
}

func (c *Command) emitStructuredData(ctx context.Context, result *output.ScanResult) {
	if result == nil || c.DataBus == nil {
		return
	}
	for _, service := range result.GOGO {
		if service != nil {
			c.EmitDataCtx(ctx, "gogo", output.ToolDataService, service.GetTarget(), service)
		}
	}
	for _, probe := range result.Spray {
		if probe != nil {
			c.EmitDataCtx(ctx, "spray", output.ToolDataWeb, probe.UrlString, probe)
		}
	}
}

var scanFileFlags = map[string]bool{
	"-l": true, "--list": true,
	"-f": true, "--file": true,
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
