package zombie

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/toolargs"
	"github.com/chainreactors/proxyclient"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
	zombiepkg "github.com/chainreactors/zombie/pkg"
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

func (c *Command) WithEvents(events aop.EventPublisher) *Command {
	c.Events = events
	return c
}

func (c *Command) Name() string { return "zombie" }

func (c *Command) Usage() string {
	var options zombiecore.Option
	return toolargs.GoFlagsHelp(c.Name(), &options)
}

func (c *Command) QuickReference() string {
	return `### zombie — weak credential checks on discovered services (authorized checks only)
  -i <ip> -s <service>          Check one service on an IP
  -I <file> / -c <cidr>         Target file, or a CIDR range
  -u <user> / -U <file>         Usernames, or a username list file
  -p <pwd> / -P <file>          Passwords, or a password list file
  -a user::pass / -A <file>     Explicit credentials, or a credential list file
  -j <file> / -g <file>         Reuse gogo or JSON results as input (not output)
  --weakpass                    Apply the built-in common weak-password rule
  --force-continue              Keep testing after a first success
  NOTE: -f/-O write results to a file (-O json for JSON Lines); -o sets the stdout format.`
}

func (c *Command) Run(ctx context.Context, execution *coretool.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("zombie", &err)
	args := execution.Args
	args = c.resolveRelativePaths(args)
	args = ensureOutputDrain(args)
	egress := coretool.ResolveExecutionEgress(execution, c.Proxy)
	proxyDial, err := proxyDialFor(egress.ProxyURL)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("zombie debug enabled")
	}
	runOpts := zombiecore.RunOptions{
		Output:    &buf,
		ProxyDial: proxyDial,
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

func proxyDialFor(proxyURL string) (zombiepkg.DialFunc, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("zombie: invalid proxy %q: %w", proxyURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("zombie: invalid proxy %q: expected URL with scheme and host", proxyURL)
	}
	dial, err := proxyclient.NewClient(parsed)
	if err != nil {
		return nil, fmt.Errorf("zombie: initialize proxy %q: %w", proxyURL, err)
	}
	return dial.DialContext, nil
}

// zombie core only starts its result consumer when a file output is present,
// while workers always publish to the result channel. Supply the null device
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
