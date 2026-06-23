package spray

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/sdk/spray"
	spraycore "github.com/chainreactors/spray/core"
)

type Command struct {
	engine  *spray.Engine
	logger  telemetry.Logger
	proxy   string
	workDir string
}

func New(engine *spray.Engine) *Command {
	return &Command{engine: engine, logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	if logger != nil {
		c.logger = logger
	}
	return c
}

func (c *Command) SetWorkDir(dir string) { c.workDir = dir }

func (c *Command) WithProxy(proxy string) *Command {
	c.proxy = proxy
	return c
}

func (c *Command) SetProxy(proxy string) { c.proxy = proxy }

func (c *Command) Name() string { return "spray" }

func (c *Command) Usage() string {
	return spraycore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (err error) {
	args = c.resolveRelativePaths(args)
	var buf bytes.Buffer
	debug := toolargs.BoolFlagEnabled(args, "--debug")
	if debug {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("spray debug enabled")
	}
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}
	args = c.injectProxy(args)
	// Use a spray-specific config name so that spray never accidentally
	// loads aiscan's own aiscan.yaml from the agent's working directory.
	if err := spraycore.RunWithArgs(ctx, withDefaultScannerFlags(args), spraycore.RunOptions{
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
	}); err != nil {
		fmt.Fprint(commands.Output, buf.String())
		return fmt.Errorf("spray: %w", err)
	}
	fmt.Fprint(commands.Output, buf.String())
	return nil
}

// TestInjectProxy is exported for cross-package testing.
func (c *Command) TestInjectProxy(args []string) []string {
	return c.injectProxy(args)
}

func (c *Command) injectProxy(args []string) []string {
	if c.proxy == "" {
		return args
	}
	if toolargs.HasFlag(args, "--proxy") {
		return args
	}
	return append(args, "--proxy", c.proxy)
}

func withDefaultNoBar(args []string) []string {
	return withDefaultBoolFlag(args, "--no-bar")
}

func withDefaultNoStat(args []string) []string {
	return withDefaultBoolFlag(args, "--no-stat")
}

func withDefaultScannerFlags(args []string) []string {
	return withDefaultNoStat(withDefaultNoBar(args))
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
	return toolargs.ResolveRelativePaths(args, sprayFileFlags, c.workDir)
}
