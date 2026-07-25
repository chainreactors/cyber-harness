package spray

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/sdk/spray"
	spraycore "github.com/chainreactors/spray/core"
	"github.com/chainreactors/utils/parsers"
)

type Command struct {
	toolargs.Base
	engine *spray.Engine
}

func New(engine *spray.Engine) *Command {
	c := &Command{engine: engine}
	c.InitLogger(nil)
	return c
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	c.InitLogger(logger)
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.Proxy = proxy
	return c
}

func (c *Command) WithDataBus(bus *eventbus.Bus[output.ToolDataEvent]) *Command {
	c.DataBus = bus
	return c
}

func (c *Command) Name() string { return "spray" }

func (c *Command) Usage() string {
	var options spraycore.Option
	return toolargs.GoFlagsHelp(c.Name(), &options)
}

func (c *Command) QuickReference() string {
	return `### spray — web probing, fingerprints, common files, and crawl
  -u <url>       Target URL (required unless -l is used)
  -l <file>      URL list file
  --finger       Enable active fingerprint detection
  --crawl        Enable web crawling
  --active       Enable active finger path probing
  -d <file>      Dictionary file for path discovery
  -j             JSON output
  Examples:
    spray -u http://target.com
    spray -u http://target.com --finger
    spray -l urls.txt --finger --crawl`
}

func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("spray", &err)
	args := execution.Args
	args = c.resolveRelativePaths(args)
	var buf bytes.Buffer
	debug := toolargs.BoolFlagEnabled(args, "--debug")
	jsonOut := toolargs.BoolFlagEnabled(args, "-j") || toolargs.BoolFlagEnabled(args, "--json")
	if debug {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("spray debug enabled")
	}
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}
	args = c.injectProxy(args)
	runOpts := spraycore.RunOptions{
		Output:        &buf,
		DefaultConfig: ".spray.yaml",
		BeforePrepare: func(option *spraycore.Option) error {
			if c.engine != nil {
				c.engine.InstallResourceProvider()
			}
			if option != nil {
				option.Quiet = !debug
			}
			return nil
		},
		AfterPrepare: func(option *spraycore.Option) error {
			if c.engine == nil {
				return nil
			}
			if err := c.engine.Init(); err != nil {
				return err
			}
			if debug {
				telemetry.EnableLogsDebug()
			}
			if option != nil && option.ActivePlugin {
				option.ActivePlugin = false
				option.ActivePlugin = true
			}
			return nil
		},
		OnResult: func(r *parsers.SprayResult) {
			c.EmitDataCtx(ctx, "spray", output.ToolDataWeb, r.UrlString, r)
			if debug {
				// spray core prints results itself when not quiet
				return
			}
			writeResult(execution.Stdout, r, jsonOut)
		},
	}
	if err := spraycore.RunWithArgs(ctx, withDefaultScannerFlags(args), runOpts); err != nil {
		fmt.Fprint(execution.Stdout, buf.String())
		return nil, fmt.Errorf("spray: %w", err)
	}
	fmt.Fprint(execution.Stdout, buf.String())
	return nil, nil
}

func writeResult(w io.Writer, result *parsers.SprayResult, jsonOutput bool) {
	if result == nil {
		return
	}
	line := result.String()
	if jsonOutput {
		line = result.ToJson()
	}
	if line != "" {
		fmt.Fprintln(w, line)
	}
}

// TestInjectProxy is exported for cross-package testing.
func (c *Command) TestInjectProxy(args []string) []string {
	return c.injectProxy(args)
}

func (c *Command) injectProxy(args []string) []string {
	if c.Proxy == "" {
		return args
	}
	if toolargs.HasFlag(args, "--proxy") {
		return args
	}
	return append(args, "--proxy", c.Proxy)
}

func withDefaultNoBar(args []string) []string {
	return withDefaultBoolFlag(args, "--no-bar")
}

func withDefaultNoStat(args []string) []string {
	return withDefaultBoolFlag(args, "--no-stat")
}

func withDefaultScannerFlags(args []string) []string {
	return withDefaultClient(withDefaultNoStat(withDefaultNoBar(args)))
}

func withDefaultClient(args []string) []string {
	for _, arg := range args {
		if arg == "-C" || strings.HasPrefix(arg, "-C=") || arg == "--client" || strings.HasPrefix(arg, "--client=") {
			return args
		}
	}
	return append(args, "--client", "req")
}

func withDefaultBoolFlag(args []string, flag string) []string {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args...)
	out = append(out, flag)
	return out
}

var sprayFileFlags = map[string]bool{
	"--resume": true, "-c": true, "--config": true,
	"-l": true, "--list": true, "--raw": true,
	"-d": true, "--dict": true, "-r": true, "--rules": true,
	"-R": true, "--append-rule": true, "--append": true,
	"-f": true, "--file": true, "--dump-file": true, "--extract-config": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, sprayFileFlags, c.WorkDir)
}
