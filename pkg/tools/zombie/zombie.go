package zombie

import (
	"bytes"
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
)

type Command struct {
	engine *sdkzombie.Engine
	logger telemetry.Logger
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

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	return zombiecore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("zombie debug enabled")
	}
	if err := zombiecore.RunWithArgs(ctx, args, zombiecore.RunOptions{
		Output: &buf,
	}); err != nil {
		return buf.String(), fmt.Errorf("zombie: %w", err)
	}
	return buf.String(), nil
}
