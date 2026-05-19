package spray

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/sdk/spray"
	spraycore "github.com/chainreactors/spray/core"
)

type Command struct {
	engine *spray.SprayEngine
	logger telemetry.Logger
	proxy  string
}

func New(engine *spray.SprayEngine) *Command {
	return &Command{engine: engine, logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	if logger != nil {
		c.logger = logger
	}
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.proxy = proxy
	return c
}

func (c *Command) Name() string { return "spray" }

func (c *Command) Usage() string {
	return spraycore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
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
	if err := spraycore.RunWithArgs(ctx, withDefaultScannerFlags(args), spraycore.RunOptions{
		Output: &buf,
		BeforePrepare: func(option *spraycore.Option) error {
			if c.engine != nil {
				c.engine.InstallResourceProvider()
			}
			if debug && option != nil {
				option.Quiet = false
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
		return buf.String(), fmt.Errorf("spray: %w", err)
	}
	return buf.String(), nil
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
