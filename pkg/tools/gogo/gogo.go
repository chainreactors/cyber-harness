package gogo

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	gogocore "github.com/chainreactors/gogo/v2/core"
	"github.com/chainreactors/sdk/gogo"
)

type Command struct {
	engine  *gogo.Engine
	logger  telemetry.Logger
	proxy   string
	workDir string
}

func New(engine *gogo.Engine) *Command {
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

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	return gogocore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (err error) {
	defer telemetry.SDKRecover("gogo", &err)
	args = c.normalizeArgs(args)
	args = c.injectProxy(args)

	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("gogo debug enabled")
	}

	var buf bytes.Buffer
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
		fmt.Fprint(commands.Output, buf.String())
		return err
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

// normalizeArgs adapts common agent-generated gogo arguments before handing
// them to the upstream parser. gogo's -j/--json is an input file, while agents
// often use it as a boolean JSON-output flag; treat valueless -j as -o jl.
func (c *Command) normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isGogoValuelessJSONFlag(arg, args, i) {
			out = append(out, "-o", "jl")
			continue
		}
		if key, value, ok := splitLongFlagValue(arg); ok {
			switch {
			case isGogoFileFlag(key):
				out = append(out, key+"="+c.resolvePathArg(value))
			case isGogoOutputFormatFlag(key):
				out = append(out, key+"="+normalizeOutputFormat(value))
			default:
				out = append(out, arg)
			}
			continue
		}
		if isGogoFileFlag(arg) {
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, c.resolvePathArg(args[i]))
			}
			continue
		}
		if isGogoOutputFormatFlag(arg) {
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, normalizeOutputFormat(args[i]))
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func (c *Command) resolvePathArg(value string) string {
	if c.workDir == "" || value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(c.workDir, value)
}

func isGogoValuelessJSONFlag(arg string, args []string, index int) bool {
	if arg != "-j" && arg != "--json" {
		return false
	}
	return index+1 >= len(args) || strings.HasPrefix(args[index+1], "-")
}

func splitLongFlagValue(arg string) (string, string, bool) {
	if !strings.HasPrefix(arg, "--") {
		return "", "", false
	}
	key, value, ok := strings.Cut(arg, "=")
	return key, value, ok
}

func isGogoFileFlag(flag string) bool {
	switch flag {
	case "-f", "--file",
		"--path",
		"-l", "-L", "--list",
		"-j", "--json",
		"-F", "--format",
		"--exclude-file",
		"--port-config",
		"--ef", "--ff":
		return true
	default:
		return false
	}
}

func isGogoOutputFormatFlag(flag string) bool {
	switch flag {
	case "-o", "--output", "-O", "--file-output":
		return true
	default:
		return false
	}
}

func normalizeOutputFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "jsonl":
		return "jl"
	default:
		return value
	}
}
