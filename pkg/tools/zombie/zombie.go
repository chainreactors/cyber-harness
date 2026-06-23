package zombie

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
)

type Command struct {
	engine  *sdkzombie.Engine
	logger  telemetry.Logger
	proxy   string
	workDir string
}

func New(engine *sdkzombie.Engine) *Command {
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

// SetProxy stores the proxy URL. Note: the zombie library's RunOptions no
// longer exposes ProxyDial, so proxy is not applied at runtime until upstream
// re-adds support.
func (c *Command) SetProxy(proxy string) { c.proxy = proxy }

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	return zombiecore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) error {
	args = c.resolveRelativePaths(args)
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("zombie debug enabled")
	}
	runOpts := zombiecore.RunOptions{Output: &buf}
	if err := zombiecore.RunWithArgs(ctx, args, runOpts); err != nil {
		if buf.Len() > 0 {
			fmt.Fprint(commands.Output, buf.String())
		}
		return fmt.Errorf("zombie: %w", err)
	}
	fmt.Fprint(commands.Output, buf.String())
	return nil
}


var zombieFileFlags = map[string]bool{
	"-I": true, "--IP": true, "-U": true, "--USER": true,
	"-P": true, "--PWD": true, "-A": true, "--AUTH": true,
	"-j": true, "--json": true, "-g": true, "--gogo": true,
	"-f": true, "--file": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, zombieFileFlags, c.workDir)
}
