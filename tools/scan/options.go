package scan

import (
	"context"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
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

type DeepBrowserFunc func(ctx context.Context, targetURL string) (string, error)

func WithParent(a *agent.Agent) Option {
	return func(c *Command) { c.parent = a }
}

func WithProxy(proxy string) Option {
	return func(c *Command) { c.Proxy = proxy }
}

func WithEvents(events aop.EventEmitter) Option {
	return func(c *Command) { c.Events = events }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Command) { c.InitLogger(logger) }
}

func (c *Command) Configure(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
}

func WithDeepBrowserFunc(fn DeepBrowserFunc) Option {
	return func(c *Command) { c.deepBrowser = fn }
}

// SkillReader reads a scan sub-skill by name (e.g. "verify", "sniper", "deep").
// Returns the skill content or "" if not found.
type SkillReader func(name string) string

func WithSkillReader(r SkillReader) Option {
	return func(c *Command) { c.readSkill = r }
}
