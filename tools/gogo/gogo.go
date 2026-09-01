package gogo

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/toolargs"
	gogocore "github.com/chainreactors/gogo/v2/core"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/utils/parsers"
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

func (c *Command) WithEvents(events aop.EventEmitter) *Command {
	c.Events = events
	return c
}

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	var options gogocore.Runner
	return toolargs.GoFlagsHelp(c.Name(), &options)
}

func (c *Command) QuickReference() string {
	return `### gogo — host, port, service, and banner discovery
  -i <ip/cidr>   Target (IP, CIDR, or comma-separated). NOT ip:port — use -i IP -p PORT.
  -p <ports>     Runtime port preset/tag/alias, range, or explicit ports (currently observed: top1, top2, top3, all, -; use -P port to list presets)
  -l <file>      Target file (one IP/CIDR per line)
  -t, --thread <n> Concurrent worker count
  -o <format>    Command-line output format (JSON Lines: jl)
  -f <file>      Output filename
  -O <format>    File output format (JSON Lines: jl)
  -j <json-file> Previous-results JSON input file (value required; not output)
  -P port        Print the current runtime port presets
  NOTE: Do not infer top100/top1000/top2k/top12k/full as port presets from another release; use -P port for the current runtime list.
  NOTE: "total ports: 1" means the normalized plan has one port; it does not mean a complete port scan.
  See aiscan://skills/aiscan/okf/easm/gogo.md for the full command contract.
  -e             Enable exploit/neutron scan
  -v             Enable active fingerprint scan
  Examples:
    gogo -i 10.0.0.1 -p top2
    gogo -i 10.0.0.0/24 -p 80,443,8080
    gogo -l targets.txt -p top2 -ev
    gogo -i 10.0.0.1 -p top2 -o jl
    gogo -i 10.0.0.1 -p top2 -f results.jsonl -O jl`
}

func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("gogo", &err)
	args := execution.Args
	args = c.normalizeArgs(args)
	egress := commands.ResolveExecutionEgress(execution, c.Proxy)
	args = c.injectProxyURL(args, egress.ProxyURL)

	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("gogo debug enabled")
	}

	var buf bytes.Buffer
	opts := gogocore.RunOptions{
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
		OnResult: func(r *parsers.GOGOResult) {
			c.EmitArtifactCtx(ctx, "gogo", toolpb.ArtifactKindService, r.GetTarget(), r)
		},
	}
	if err := gogocore.RunWithArgs(ctx, args, opts); err != nil {
		fmt.Fprint(execution.Stdout, buf.String())
		return nil, err
	}
	fmt.Fprint(execution.Stdout, buf.String())
	return nil, nil
}

// TestInjectProxy is exported for cross-package testing.
func (c *Command) TestInjectProxy(args []string) []string {
	return c.injectProxy(args)
}

func (c *Command) injectProxy(args []string) []string {
	return c.injectProxyURL(args, c.Proxy)
}

func (c *Command) injectProxyURL(args []string, proxy string) []string {
	if proxy == "" {
		return args
	}
	if toolargs.HasFlag(args, "--proxy") {
		return args
	}
	return append(args, "--proxy", proxy)
}

// normalizeArgs adapts common agent-generated gogo arguments before handing
// them to the upstream parser. gogo's -j/--json is an input file, while older
// agents sometimes used it as a boolean JSON-output flag; treat valueless -j
// as -o jl only as a compatibility fallback. The canonical prompt contract
// tells agents to use gogo's native flags explicitly.
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
