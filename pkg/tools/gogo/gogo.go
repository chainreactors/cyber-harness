package gogo

import (
	"bytes"
	"context"
	"fmt"

	gogocore "github.com/chainreactors/gogo/v2/core"
	"github.com/chainreactors/sdk/gogo"
)

type Command struct {
	engine *gogo.GogoEngine
}

func New(engine *gogo.GogoEngine) *Command {
	return &Command{engine: engine}
}

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	return gogocore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	var buf bytes.Buffer
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}
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
