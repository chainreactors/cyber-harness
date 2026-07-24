package zombie

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
)

type Command struct {
	toolargs.Base
	engine *sdkzombie.Engine
}

func New(engine *sdkzombie.Engine) *Command {
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

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	var options zombiecore.Option
	return toolargs.GoFlagsHelp(c.Name(), &options)
}

func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("zombie", &err)
	args := execution.Args
	args = c.resolveRelativePaths(args)
	args = ensureOutputDrain(args)
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("zombie debug enabled")
	}
	runOpts := zombiecore.RunOptions{
		Output: &buf,
	}
	if err := zombiecore.RunWithArgs(ctx, args, runOpts); err != nil {
		if buf.Len() > 0 {
			fmt.Fprint(execution.Stdout, buf.String())
		}
		return nil, fmt.Errorf("zombie: %w", err)
	}
	fmt.Fprint(execution.Stdout, buf.String())
	return nil, nil
}

// zombie core only starts its result consumer when a file output is present,
// while workers always publish to the result channel. Supply the system sink
// for normal stdout-only runs so successful and failed attempts cannot deadlock.
func ensureOutputDrain(args []string) []string {
	if toolargs.HasFlag(args, "-f") || toolargs.HasFlag(args, "--file") {
		return args
	}
	return append(append([]string(nil), args...), "--file", os.DevNull)
}

var zombieFileFlags = map[string]bool{
	"-I": true, "--IP": true, "-U": true, "--USER": true,
	"-P": true, "--PWD": true, "-A": true, "--AUTH": true,
	"-j": true, "--json": true, "-g": true, "--gogo": true,
	"-f": true, "--file": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, zombieFileFlags, c.WorkDir)
}
