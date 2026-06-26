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
	toolargs.Base
	engine *gogo.Engine
}

func New(engine *gogo.Engine) *Command {
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

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	return gogocore.Help()
}

func (c *Command) QuickReference() string {
	return `### gogo — host, port, service, and banner discovery
  -i <ip/cidr>   Target (IP, CIDR, or comma-separated). NOT ip:port — use -i IP -p PORT.
  -p <ports>     Presets: top1, top2, top100, top1000, all, - (65535), or 80,443,8080
  -l <file>      Target file (one IP/CIDR per line)
  -o jl          JSON Lines output (do NOT use -j for JSON output; -j is a JSON input file)
  -e             Enable exploit/neutron scan
  -v             Enable active fingerprint scan
  Examples:
    gogo -i 10.0.0.1 -p top100
    gogo -i 10.0.0.0/24 -p 80,443,8080
    gogo -l targets.txt -p top2 -ev`
}

func (c *Command) Execute(ctx context.Context, args []string) (err error) {
	args = c.normalizeArgs(args)
	args = c.injectProxy(args)

	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("gogo debug enabled")
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
	if c.Proxy == "" {
		return args
	}
	if toolargs.HasFlag(args, "--proxy") {
		return args
	}
	return append(args, "--proxy", c.Proxy)
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
	if c.WorkDir == "" || value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(c.WorkDir, value)
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
