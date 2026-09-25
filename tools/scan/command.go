package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/cyber/tools/scan/pipeline"
	"github.com/chainreactors/cyber/tools/toolargs"
	"github.com/chainreactors/utils/parsers"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	executionOnly bool
	toolargs.Base
	engines       *engine.Set
	worker        Worker
	verifyDefault string
	hasModel      func(context.Context) bool
}

type flags struct {
	Inputs          []string `short:"i" long:"input" description:"Input target: URL, IP, IP:port, or CIDR"`
	ListFile        string   `short:"l" long:"list" description:"File containing input targets, one per line"`
	Mode            string   `long:"mode" description:"Scan profile: quick or full" default:"quick"`
	Thread          int      `long:"thread" description:"Total concurrency budget distributed across engines" default:"1000"`
	Sniper          bool     `long:"sniper" description:"Use AI to search public vulnerabilities for discovered fingerprints"`
	Trace           bool     `long:"trace" description:"Show internal scanner source and pipeline trace"`
	Debug           bool     `long:"debug" description:"Enable trace and underlying scanner debug logs"`
	JSON            bool     `short:"j" long:"json" description:"Output raw gogo and spray results as JSON Lines (direct gogo uses -o jl)"`
	NoColor         bool     `long:"no-color" description:"Disable ANSI colors in terminal output"`
	Ports           string   `long:"ports" description:"Runtime gogo port preset/tag/alias, range, or explicit ports; defaults to all in quick and - in full"`
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
	Verify          string   `long:"verify" description:"Verify all vulnerabilities and weak passwords with AI: on or off (default: on when a model is configured)"`
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

func (c *Command) Name() string { return "scan" }

func (c *Command) Usage() string {
	return Usage()
}

func (c *Command) QuickReference() string {
	return `### scan — the full pipeline: gogo -> spray -> zombie -> neutron
  -i <target>          URL, IP, IP:port, or CIDR  (-l <file> for a list)
  --mode quick|full    Scan profile (default quick)
  --ports <preset>     gogo port preset; defaults to all in quick, - in full
  --verify on|off      Verify all vulnerabilities and weak passwords
  --sniper             AI vulnerability search for fingerprints
  -j                   Emit raw gogo and spray results as JSON Lines
  Examples:
    scan -i 10.0.0.0/24 --mode quick
    scan -i https://target --mode full --verify on`
}

func Usage() string {
	var options flags
	return toolargs.GoFlagsHelp("scan", &options)
}

func (c *Command) Run(ctx context.Context, execution *coretool.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("scan", &err)
	egress := coretool.ResolveExecutionEgress(execution, c.Proxy)
	ctx = withInvocationProxy(ctx, egress.ProxyURL)
	// Command output is consumed by agents and remote hosts as plain text.
	args := toolargs.ResolveRelativePaths(execution.Args, scanFileFlags, c.WorkDir)
	if !slices.Contains(args, "--no-color") {
		args = append(slices.Clone(args), "--no-color")
	}
	out, err := c.execute(ctx, args, execution.Stdout)
	if out != "" {
		fmt.Fprint(execution.Stdout, out)
	}
	// Structured scanner records are emitted through the artifact stream.
	// Returning the collector's private aggregation here would leak a second
	// result schema through AOP tool.result.
	return nil, err
}

func (c *Command) execute(ctx context.Context, args []string, stream io.Writer) (string, error) {
	var flags flags
	parser := toolargs.NewGoFlagsParser("scan", &flags)
	if _, err := parser.ParseArgs(args); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return c.Usage() + "\n", nil
		}
		return "", fmt.Errorf("scan: %w", err)
	}
	if c.executionOnly && (flags.Sniper || (flags.Verify != "" && flags.Verify != "off")) {
		return "", fmt.Errorf("scan: AI modes are unavailable in execution-only mode")
	}
	if flags.Debug {
		flags.Trace = true
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("scanner debug enabled")
	}
	profile, err := profileForMode(flags.Mode)
	if err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	if parser.FindOptionByLongName("verify").IsSet() && flags.Verify == "" {
		return "", fmt.Errorf("scan: --verify requires on or off")
	}
	verify := flags.Verify
	if verify == "" {
		verify = c.verifyDefault
	}
	if err := ValidateVerify(verify); err != nil {
		return "", err
	}
	available := !c.executionOnly && c.worker != nil && c.hasModel != nil && c.hasModel(ctx)
	if verify == "" {
		if available {
			verify = "on"
		} else {
			verify = "off"
		}
	}
	if (verify == "on" || flags.Sniper) && !available {
		return "", fmt.Errorf("scan: AI verification and sniper require a configured model provider")
	}
	rawInputs, err := readInputs(flags.Inputs, flags.ListFile)
	if err != nil {
		return "", err
	}
	if len(rawInputs) == 0 {
		return "", fmt.Errorf("scan: no input targets")
	}

	if flags.JSON {
		stream = nil
	}

	trace := flags.Trace || flags.Debug
	coll := newCollector(rawInputs, stream, stream != nil && !flags.NoColor, trace)
	observe := func(observation pipeline.Observation[event]) {
		coll.Observe(observation)
		if trace && stream != nil {
			if traceLine := formatTraceEvent(observation); traceLine != "" {
				fmt.Fprintln(stream, traceLine)
			}
		}
	}

	seeds := buildSeedEvents(rawInputs, func(raw string) {
		observe(pipeline.Observation[event]{
			Action: pipeline.ActionAccept,
			Event:  errorEventOf("", fmt.Sprintf("skip invalid input: %s", raw)),
		})
	})
	if len(seeds) == 0 {
		return "", fmt.Errorf("scan: no valid inputs")
	}

	capabilities := c.buildCapabilities(flags, profile)
	selected := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		selected = append(selected, capability.Name)
	}
	slices.Sort(selected)
	var unavailable []string
	for name := range profile.Capabilities {
		if !slices.Contains(selected, name) {
			unavailable = append(unavailable, name)
		}
	}
	slices.Sort(unavailable)
	if !flags.JSON {
		line := "selected checks: " + strings.Join(selected, ", ") + "; conditional checks run only for matching inputs"
		if len(unavailable) > 0 {
			line += "\nunavailable or disabled checks: " + strings.Join(unavailable, ", ")
		}
		if stream != nil {
			fmt.Fprintln(stream, line)
		} else {
			coll.fileLines = append(coll.fileLines, line)
		}
	}
	if len(capabilities) == 0 {
		return "", fmt.Errorf("scan: no scanning capabilities available")
	}
	p, err := pipeline.New(ctx, capabilities, observe)
	if err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	p.Run(seeds)

	if verify == "on" {
		runVerifyPass(ctx, c.worker, coll, c.Logger)
	}
	if flags.Sniper {
		runSniperPass(ctx, c.worker, coll, c.Logger)
	}

	// Run has joined all workers. Preserve evidence after cancellation.
	runErr := ctx.Err()
	if runErr != nil {
		coll.errors = append(coll.errors, runErr.Error())
		coll.canceled = true
	}
	coll.Finish()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if emitErr := c.emitStructuredData(finishCtx, coll); emitErr != nil {
		coll.errors = append(coll.errors, emitErr.Error())
	}
	if runErr == nil && len(coll.errors) > 0 {
		runErr = errors.New(strings.Join(coll.errors, "; "))
	}
	var out string
	if flags.JSON {
		out, err = formatJSONLines(coll)
	} else {
		out = formatSummary(coll, stream != nil && !flags.NoColor)
	}
	return out, errors.Join(runErr, err)
}

func (c *Command) emitStructuredData(ctx context.Context, coll *collector) (err error) {
	if coll == nil || c.Events == nil {
		return nil
	}
	coll.mu.Lock()
	services := append([]*parsers.GOGOResult(nil), coll.gogoResults...)
	probes := append([]*parsers.SprayResult(nil), coll.sprayResults...)
	artifacts := append([]artifactResult(nil), coll.artifacts...)
	loots := append([]parsers.Loot(nil), coll.loots...)
	coll.mu.Unlock()
	for _, service := range services {
		if service == nil {
			continue
		}
		resultID := toolargs.ArtifactResultID("gogo", toolpb.ArtifactKindService, service.GetTarget(), service)
		err = errors.Join(err, c.EmitArtifactResultCtx(ctx, resultID, "gogo", toolpb.ArtifactKindService, service.GetTarget(), service))
	}
	for _, probe := range probes {
		if probe == nil {
			continue
		}
		resultID := toolargs.ArtifactResultID("spray", toolpb.ArtifactKindWeb, probe.UrlString, probe)
		err = errors.Join(err, c.EmitArtifactResultCtx(ctx, resultID, "spray", toolpb.ArtifactKindWeb, probe.UrlString, probe))
	}
	for _, artifact := range artifacts {
		err = errors.Join(err, c.EmitArtifactResultCtx(ctx, artifact.ResultID, artifact.Tool, artifact.Kind, artifact.Target, artifact.Data))
	}
	for _, loot := range loots {
		resultID, _ := loot.Data["result_id"].(string)
		tool, _ := loot.Data["artifact_tool"].(string)
		if resultID == "" || tool == "" {
			c.Logger.Warnf("skip unbound %s loot for %s", loot.Kind, loot.Target)
			continue
		}
		verificationStatus, _ := loot.Data["verification_status"].(string)
		err = errors.Join(err, c.EmitLootCtx(
			ctx,
			resultID,
			tool,
			loot.Kind,
			loot.Target,
			loot.Priority,
			loot.Description,
			verificationStatus,
			loot.Tags,
		))
	}
	return err
}

var scanFileFlags = map[string]bool{
	"-l": true, "--list": true,
	"-f": true, "--file": true,
	"--dict": true, "--rule": true,
}
