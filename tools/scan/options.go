package scan

import (
	"context"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
)

type invocationProxyKey struct{}

func withInvocationProxy(ctx context.Context, proxy string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationProxyKey{}, proxy)
}

func (c *Command) proxyForContext(ctx context.Context) string {
	if ctx == nil {
		return c.Proxy
	}
	if proxy, ok := ctx.Value(invocationProxyKey{}).(string); ok {
		return proxy
	}
	return c.Proxy
}

type Option func(*Command)

func WithParent(a *agent.Agent) Option {
	return func(c *Command) { c.parent = a }
}

func WithProxy(proxy string) Option {
	return func(c *Command) { c.Proxy = proxy }
}

func WithEvents(events aop.EventPublisher) Option {
	return func(c *Command) { c.Events = events }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Command) { c.InitLogger(logger) }
}

func WithDeepBrowserFunc(fn func(context.Context, string) (string, error)) Option {
	return func(c *Command) { c.deepBrowser = fn }
}

func WithSkillReader(read func(string) string) Option {
	return func(c *Command) { c.readSkill = read }
}
