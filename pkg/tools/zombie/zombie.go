package zombie

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/proxyclient"
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

func (c *Command) SetProxy(proxy string) { c.proxy = proxy }

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	return zombiecore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	args = c.resolveRelativePaths(args)
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("zombie debug enabled")
	}
	runOpts := zombiecore.RunOptions{Output: &buf}
	if c.proxy != "" {
		proxyURL, err := url.Parse(c.proxy)
		if err == nil {
			dial, dialErr := proxyclient.NewClient(proxyURL)
			if dialErr == nil {
				runOpts.ProxyDial = dial.DialContext
			}
		}
	}
	if err := zombiecore.RunWithArgs(ctx, args, runOpts); err != nil {
		return buf.String(), fmt.Errorf("zombie: %w", err)
	}
	return buf.String(), nil
}


// resolveRelativePaths resolves relative file arguments against workDir.
func (c *Command) resolveRelativePaths(args []string) []string {
	if c.workDir == "" {
		return args
	}
	fileFlags := map[string]bool{
		"-I":     true,
		"--IP":   true,
		"-U":     true,
		"--USER": true,
		"-P":     true,
		"--PWD":  true,
		"-A":     true,
		"--AUTH": true,
		"-j":     true,
		"--json": true,
		"-g":     true,
		"--gogo": true,
		"-f":     true,
		"--file": true,
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if key, value, ok := strings.Cut(arg, "="); ok {
			if fileFlags[key] {
				out = append(out, key+"="+c.resolvePath(value))
				continue
			}
			out = append(out, arg)
			continue
		}
		if fileFlags[arg] && i+1 < len(args) {
			out = append(out, arg)
			i++
			out = append(out, c.resolvePath(args[i]))
			continue
		}
		out = append(out, arg)
	}
	return out
}

func (c *Command) resolvePath(value string) string {
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(c.workDir, value)
}
