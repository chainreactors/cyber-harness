package gogo

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	gogocore "github.com/chainreactors/gogo/v2/core"
	"github.com/chainreactors/sdk/gogo"
)

type Command struct {
	engine *gogo.GogoEngine
	logger telemetry.Logger
	proxy  string
}

func New(engine *gogo.GogoEngine) *Command {
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

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	return gogocore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("gogo debug enabled")
	}
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}
	args = c.injectProxy(args)
	if err := gogocore.RunWithArgs(ctx, args, gogocore.RunOptions{
		Output: &buf,
		BeforeInit: func() error {
			if c.engine != nil {
				c.engine.InstallResourceProvider()
			}
			return nil
		},
		AfterInit: func() error {
			if c.engine == nil {
				return nil
			}
			return c.engine.Init()
		},
	}); err != nil {
		return buf.String(), fmt.Errorf("gogo: %w", err)
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
