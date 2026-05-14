package spray

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/sdk/spray"
	spraycore "github.com/chainreactors/spray/core"
)

type Command struct {
	engine *spray.SprayEngine
}

func New(engine *spray.SprayEngine) *Command {
	return &Command{engine: engine}
}

func (c *Command) Name() string { return "spray" }

func (c *Command) Usage() string {
	return spraycore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	var buf bytes.Buffer
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}
	if err := spraycore.RunWithArgs(ctx, withDefaultScannerFlags(args), spraycore.RunOptions{
		Output: &buf,
		BeforePrepare: func(option *spraycore.Option) error {
			if c.engine != nil {
				c.engine.InstallResourceProvider()
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
