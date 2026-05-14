package zombie

import (
	"bytes"
	"context"
	"fmt"

	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
)

type Command struct {
	engine *sdkzombie.Engine
}

func New(engine *sdkzombie.Engine) *Command {
	return &Command{engine: engine}
}

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	return zombiecore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	var buf bytes.Buffer
	if err := zombiecore.RunWithArgs(ctx, args, zombiecore.RunOptions{
		Output: &buf,
	}); err != nil {
		return buf.String(), fmt.Errorf("zombie: %w", err)
	}
	return buf.String(), nil
}
