package zombie

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/proxyclient"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiecore "github.com/chainreactors/zombie/core"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

type Command struct {
	engine *sdkzombie.Engine
	logger telemetry.Logger
	proxy  string
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

func (c *Command) WithProxy(proxy string) *Command {
	c.proxy = proxy
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
	restore := c.installProxy()
	defer restore()
	if err := zombiecore.RunWithArgs(ctx, args, zombiecore.RunOptions{
		Output: &buf,
	}); err != nil {
		return buf.String(), fmt.Errorf("zombie: %w", err)
	}
	return buf.String(), nil
}

// TestInstallProxy is exported for cross-package testing.
func (c *Command) TestInstallProxy() func() {
	return c.installProxy()
}

func (c *Command) installProxy() func() {
	if c.proxy == "" {
		return func() {}
	}
	proxyURL, err := url.Parse(c.proxy)
	if err != nil {
		c.logger.Warnf("zombie: invalid proxy URL %q: %v", c.proxy, err)
		return func() {}
	}
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		c.logger.Warnf("zombie: proxy dial setup failed: %v", err)
		return func() {}
	}
	prev := zombiepkg.ProxyDialTimeout
	zombiepkg.ProxyDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return dial.DialContext(ctx, network, address)
	}
	return func() { zombiepkg.ProxyDialTimeout = prev }
}
